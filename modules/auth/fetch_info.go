package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iptv-spider-sh/global"
	"iptv-spider-sh/model"
	"iptv-spider-sh/store"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/golang-module/carbon"
	"go.uber.org/zap"
)

// fetchResult 单个频道的节目单拉取结果。
type fetchResult int

const (
	fetchOK             fetchResult = iota // 成功写入新数据
	fetchEmpty                             // 接口正常返回但没有节目数据
	fetchFailed                            // 请求无响应、非 200、解析失败等
	fetchSessionExpired                    // 返回 HTML 认证页
)

// respKind 把一次 HTTP 响应归为三类，避免把网络/服务端故障误判成会话失效。
type respKind int

const (
	respJSON respKind = iota
	respSessionExpired
	respBad
)

// classifyResponse 区分"正常 JSON / 返回认证页 / 请求失败"。
//
// 区分它主要是为了日志与结论准确：超时、502、网关错误页都是"请求失败"，
// 重试或重新认证都没有意义；只有 200 且 body 是 HTML 认证页，才说明
// 会话在拉取过程中失效（每轮开头刚认证过，这种情况只可能是被服务端踢掉）。
func classifyResponse(resp *resty.Response) respKind {
	if resp == nil {
		return respBad
	}
	body := bytes.TrimSpace(resp.Body())
	if ct := strings.ToLower(resp.Header().Get("Content-Type")); strings.Contains(ct, "json") {
		return respJSON
	}
	if len(body) > 0 && (body[0] == '{' || body[0] == '[') {
		return respJSON
	}
	if resp.StatusCode() != http.StatusOK || len(body) == 0 {
		return respBad
	}
	return respSessionExpired
}

// fetchChannelListLocked 拉取频道信息列表，调用方必须已持有 opMu 并完成认证。
//
// 不再做会话检测与"重认证后重试"：认证刚刚在本轮开头完成，会话必然是新的。
// 失败只记结果，由下一个 cron 周期重来——旧数据原样保留，清理任务会因
// 本轮失败而跳过（见 fetch_state.go）。
func (c *Client) fetchChannelListLocked() {
	global.LOG.Info("开始更新频道信息列表")
	uri := fmt.Sprintf("%s/%s", c.EPGHostUrl, "function/ajax/epg7getChannelByAjax.jsp")
	resp := c.httpClient.Request(uri, "POST", map[string]string{
		"action": "getChannelList",
		"cateID": "000406",
	})
	switch classifyResponse(resp) {
	case respBad:
		global.LOG.Error("频道列表接口请求失败, 放弃本轮更新")
		markFetchFailed(dataChannelInfo, "请求失败")
		return
	case respSessionExpired:
		global.LOG.Error("频道列表接口返回 HTML 认证页 (认证刚完成, 会话却已失效, 可能被服务端踢掉), 放弃本轮更新")
		markFetchFailed(dataChannelInfo, "会话失效")
		return
	}
	var respJson model.JsonResponse[model.ChannelInfo]
	if err := json.Unmarshal(resp.Body(), &respJson); err != nil {
		global.LOG.Error("FetchChannelList Unmarshal Err: " + err.Error())
		markFetchFailed(dataChannelInfo, "解析失败")
		return
	}
	// 接口在参数错误/权限不足时仍返回 HTTP 200，业务错误只体现在
	// errCode/errMsg 里，之前完全不检查，会被当成"没有数据"。
	errInfo := respJson.ErrInfo()
	if errInfo != "" {
		global.LOG.Warn("FetchChannelList 接口返回错误信息: " + errInfo)
	}
	if len(respJson.Data) == 0 {
		if errInfo == "" {
			global.LOG.Error("FetchChannelList Err: No Data!")
		}
		markFetchFailed(dataChannelInfo, "接口返回空数据")
		return
	}
	global.LOG.Info(fmt.Sprintf("FetchChannelList Data Length: %d", len(respJson.Data)))
	for _, info := range respJson.Data {
		global.LOG.Debug("FetchChannelList Data:", zap.Any("Channel Info", info))
	}
	// 数据写入内存存储（按 MixNo 覆盖写入）；分组规则随每轮写入刷新，
	// config 里改了 group_rules 无需重启，下一轮拉取自动生效
	store.Default.UpsertChannelInfos(respJson.Data, global.CONFIG.Epg.GroupRules)
	markFetchOK(dataChannelInfo)
	global.LOG.Info("频道信息列表更新完成")
	global.LOG.Info(fmt.Sprintf("内存频道信息: %d 条, 频道详情: %d 条",
		store.Default.ChannelInfoCount(), store.Default.ChannelCount()))
}

// fetchChannelProgLocked 拉取全部可显示频道的节目单，调用方必须已持有 opMu
// 并完成认证。
func (c *Client) fetchChannelProgLocked() {
	// 频道信息列表没拉到（本轮列表失败或上一轮就没有数据）时，节目单无从下手。
	// 单独说明原因，避免日志里出现"成功 0, 无数据 0, 失败 0"这种看不出所以然的汇总。
	if store.Default.ChannelInfoCount() == 0 {
		global.LOG.Warn("频道信息列表为空, 跳过本轮节目单拉取")
		markFetchFailed(dataProg, "频道信息列表为空")
		return
	}
	global.LOG.Info("开始更新节目信息列表")

	uri := fmt.Sprintf("%s/%s", c.EPGHostUrl, "function/ajax/epg7getChannelByAjax.jsp")
	success, empty, failed, expired := c.runProgRound(uri, carbon.Now())

	global.LOG.Info(fmt.Sprintf("节目单更新完成: 成功 %d, 无数据 %d, 失败 %d, 会话失效 %d, 内存节目单共 %d 条",
		success, empty, failed, expired, store.Default.EPGCount()))
	if expired > 0 {
		global.LOG.Warn(fmt.Sprintf("有 %d 个频道返回 HTML 认证页(会话中途失效), 本轮数据不完整, 等待下一个周期重试", expired))
	}

	// 记录本轮结果供清理任务做前置校验（见 fetch_state.go）：
	// 一个频道都没成功、或出现会话失效，都算"本轮未成功"。
	switch {
	case success == 0:
		markFetchFailed(dataProg, fmt.Sprintf("本轮无一频道拉取成功 (无数据 %d, 失败 %d)", empty, failed))
	case expired > 0:
		markFetchFailed(dataProg, fmt.Sprintf("有 %d 个频道会话失效", expired))
	default:
		markFetchOK(dataProg)
	}
}

// runProgRound 收集待更新频道并并发拉取一轮，返回各结果计数。
func (c *Client) runProgRound(uri string, now carbon.Carbon) (success, empty, failed, expired int) {
	channelInfoList := store.Default.AggregateChannelInfosByCommName()

	// 每轮全量拉取所有频道组（同名多清晰度已在上一步聚合），
	// 去重交给 cron 间隔保证（数据新鲜度上限 = cron 间隔）。
	pending := channelInfoList
	if len(pending) == 0 {
		return
	}
	global.LOG.Info(fmt.Sprintf("待更新节目单频道数: %d", len(pending)))

	// 有限并发拉取（替代原先的串行 + 500ms sleep），
	// 并发数不宜过大，避免对 EPG 服务器造成压力
	const maxConcurrency = 4
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, ch := range pending {
		wg.Add(1)
		go func(ch model.ChannelInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var r fetchResult
			switch r = c.fetchProgForChannel(uri, ch, now); r {
			case fetchOK:
				mu.Lock()
				success++
				mu.Unlock()
			case fetchEmpty:
				mu.Lock()
				empty++
				mu.Unlock()
			case fetchSessionExpired:
				mu.Lock()
				expired++
				mu.Unlock()
			default:
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}(ch)
	}
	wg.Wait()
	return
}

// fetchProgForChannel 拉取单个频道的节目单并写入内存存储。
// 返回本轮该频道的拉取结果（成功 / 无数据 / 失败）。
func (c *Client) fetchProgForChannel(uri string, ch model.ChannelInfo, now carbon.Carbon) fetchResult {
	// 请求后稍作停顿，避免并发时对 EPG 服务器造成压力
	defer time.Sleep(time.Millisecond * 200)

	endTime := now.AddDays(3).TimestampMilli()
	startTime := now.SubDays(7).TimestampMilli()
	params := map[string]string{
		"action":    "getChannelProg",
		"code":      ch.Code,
		"channelID": ch.ChID,
		"endTime":   strconv.FormatInt(endTime, 10),
		"startTime": strconv.FormatInt(startTime, 10),
		"offset":    "0",
		"limit":     "2000",
	}
	resp := c.httpClient.Request(uri, "POST", params)
	var respJson model.JsonResponse[model.EPGDetails]
	switch classifyResponse(resp) {
	case respBad:
		// 网络错误 / 非 200 / 空 body：这是请求失败，不是会话失效。
		code := 0
		if resp != nil {
			code = resp.StatusCode()
		}
		global.LOG.Error("FetchChannelProg Err: 请求失败或响应为空",
			zap.String("CommName", ch.CommName),
			zap.Int("StatusCode", code),
			zap.Any("Params", params))
		return fetchFailed
	case respSessionExpired:
		// HTML 认证页说明会话在本轮拉取途中被服务端终止，交由整轮汇总统计。
		// 用 Debug：连续上百条会刷屏，真正的结论由汇总日志给出。
		global.LOG.Debug("FetchChannelProg: 响应为 HTML 认证页(会话中途失效)",
			zap.Any("CommName", ch.CommName),
			zap.Any("Params", params))
		return fetchSessionExpired
	}
	if err := json.Unmarshal(resp.Body(), &respJson); err != nil {
		global.LOG.Error("FetchChannelProg Unmarshal Err: "+err.Error(),
			zap.Any("SessionID", c.JSESSIONID),
			zap.Any("Params", params))
		// 单个频道解析失败不影响其他频道的更新
		return fetchFailed
	}
	if len(respJson.Data) == 0 {
		// 业务错误（errCode/errMsg）以前被当成"无数据"，日志里只能看到
		// "无数据 1" 而不知道原因，这里把服务端给的说明打出来。
		if msg := respJson.ErrInfo(); msg != "" {
			// 051002 之类表示"该频道确实没有节目单"（新上线、试播、只在
			// 特定时段播出），属于无数据而不是故障。计为失败会让每轮汇总
			// 凭空多出一条失败，真正的请求错误反而被稀释。
			if respJson.IsNoData() {
				global.LOG.Info("FetchChannelProg: 该频道无节目单数据",
					zap.String("CommName", ch.CommName),
					zap.String("Err", msg))
				return fetchEmpty
			}
			global.LOG.Warn("FetchChannelProg: 接口返回错误信息",
				zap.String("CommName", ch.CommName),
				zap.String("Err", msg))
			return fetchFailed
		}
		global.LOG.Debug("FetchChannelProg: No Data!",
			zap.Any("SessionID", c.JSESSIONID),
			zap.Any("Params", params))
		return fetchEmpty
	}
	// 避免节目时间重合问题，用新数据整体替换旧数据
	daysAgo := now.SubDays(7).TimestampMilli()
	length := 0
	var des []model.EPGDetails
	for _, details := range respJson.Data {
		if details.EndTime < daysAgo {
			continue
		}
		length++
		des = append(des, details)
	}
	store.Default.ReplaceEPG(ch.CommName, des)
	global.LOG.Debug("GetChannelProg: ",
		zap.Any("CommName", ch.CommName),
		zap.Any("Data Length", length))
	return fetchOK
}

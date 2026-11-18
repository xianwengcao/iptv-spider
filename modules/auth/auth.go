package auth

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"go.uber.org/zap"
	"iptv-spider-sh/global"
	"iptv-spider-sh/model"
	"iptv-spider-sh/modules/http_client"
	"iptv-spider-sh/modules/jsvm"
	"iptv-spider-sh/store"
	"iptv-spider-sh/utils"
	"net/http"
	"net/url"
	"sync"
)

var (
	ErrUserID  = errors.New("错误的userID")
	ErrMacAddr = errors.New("错误的Mac地址")
	ErrSNCode  = errors.New("错误的SN码")
	ErrIPAddr  = errors.New("错误的IP地址")
)

// Default4kLogAuthAddr 4K 认证服务器默认地址。
// 实际使用 stb.auth_host 配置值，仅在配置为空时回退到此处。
const Default4kLogAuthAddr = "222.68.208.73:7001"

// DefaultSTBType 机顶盒型号默认值（portal 认证时作为 stbtype 参数提交）。
// 实际使用 stb.type 配置值，仅在配置为空时回退到此处。
const DefaultSTBType = "B860A"

var globalClient *Client

type baseConfig struct {
	userID           string
	sn               string
	ip               string
	macAddr          string
	stbType          string
	userAgent        string
	pre4kLogAuthAddr string
}

type Client struct {
	// opMu 串行化认证与拉取等所有会话相关操作。
	//
	// 认证与拉取共用同一个 HttpClient 的 cookies（JSESSIONID），并发执行会
	// 互相覆盖。整轮任务（run）持有此锁完成"认证 → 频道列表 → 节目单"，
	// 不会有两个流程同时改写会话。
	// 已持锁的内部路径使用 *Locked 变体，避免重入死锁。
	opMu sync.Mutex

	jsVM       *jsvm.VM
	httpClient *http_client.HttpClient
	baseConfig
	model.AuthInfo
}

// ---------------------------------------------------------------- 对外入口

// RunRound 执行一轮完整动作：认证 → 频道信息列表 → 节目单。
//
// 为什么每轮都重新认证：会话只在拉取的那十几秒里被用到，两轮之间相隔
// 数小时，EPG 侧的 JSESSIONID 早已过期。与其花心跳维持一个用不到的会话、
// 再写一套"会话到底还活着吗"的探测逻辑，不如每轮开头直接认证一次
// （不到 1 秒），让会话变成轮内的临时资源。
//
// 认证失败即跳过本轮：此时没有任何可用会话，继续拉取必然全部失败。
// 旧数据原样保留，由清理任务的前置校验（allowClean）保证不被误删。
func (c *Client) RunRound() { c.run(true, true) }

// run 持锁执行一轮动作，整轮的写入合并为一次版本号递增。
func (c *Client) run(withList, withProg bool) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	// 一轮里的所有写入（频道快照 + 列表 + 100+ 条节目单）共用一次版本号
	// 递增，下游的 m3u/epg 缓存只切换一次，不会在拉取的十几秒内反复失效。
	store.Default.BeginBatch()
	defer store.Default.EndBatch()

	channels, err := c.startAuthLocked()
	if err != nil {
		global.LOG.Error("本轮认证失败, 跳过本轮, 保留现有数据: " + err.Error())
		if withList {
			markFetchFailed(dataChannelInfo, "认证失败")
		}
		if withProg {
			markFetchFailed(dataProg, "认证失败")
		}
		return
	}
	// 认证的两步都成功后才同步频道快照：认证中途失败时内存里的
	// 频道详情保持不变，不会出现"流程一成功、流程二失败"的半更新状态。
	c.syncChannels(channels)

	if withList {
		c.fetchChannelListLocked()
	}
	if withProg {
		c.fetchChannelProgLocked()
	}
}

// ---------------------------------------------------------------- 认证流程

// authSetupOne 认证流程一：4K 登录 → 回调 → OTT 认证 → 解析频道列表。
// 任一步骤未获得有效响应文档时立即失败——之前 nil document 会静默传给
// 下一步，最终在深处解引用 panic。
//
// 只解析、不写存储：返回最后一步的文档（供流程二提交表单）与解析到的
// 频道集合，认证全部成功后才由调用方落库。
func (c *Client) authSetupOne() (*goquery.Document, []model.Channel, error) {
	global.LOG.Info("认证流程一")
	doc := c.pre4kLogAuth()
	doc = c.r4kLogAuth(doc)
	doc = c.ottAuth(doc)
	if doc == nil {
		return nil, nil, errors.New("认证流程一失败: 未获得有效响应文档 (检查网络连通性与认证服务器地址)")
	}
	channels := c.processChannel(doc)
	// 解析到 0 个频道说明认证页结构变化或频道列表为空。此时不能当成功：
	// 会出现"认证成功"但频道表是空的，直到生成 m3u 时才发现每个频道
	// 都"未找到频道详情"。但也不宜直接判定认证失败——会话本身
	// （EPGHostUrl / JSESSIONID）是有效的，EPG 拉取并不依赖频道列表；
	// 而且直接失败会让内存里已有的频道数据也没机会继续使用。
	// 折中：本地已有频道数据时保留旧数据继续认证流程，否则按认证失败处理。
	if len(channels) == 0 {
		if store.Default.ChannelCount() == 0 {
			return nil, nil, errors.New("认证流程一失败: 未从认证页面解析到任何频道, 且本地无历史频道数据")
		}
		global.LOG.Error("认证流程一: 未从认证页面解析到任何频道, 保留原有的频道详情继续认证")
	}
	return doc, channels, nil
}

// syncChannels 用本次认证解析到的快照整体替换频道播放详情。
func (c *Client) syncChannels(channels []model.Channel) {
	if len(channels) == 0 {
		return
	}
	// 频道详情是认证页面的一份快照：整体替换而非累加，
	// 新快照里不存在的条目即为已下线频道，就地剔除（无需按时间戳猜）
	before := store.Default.ChannelCount()
	added, removed, synced := store.Default.ReplaceChannels(channels)
	if synced {
		global.LOG.Info(fmt.Sprintf("频道详情已同步: 本次解析 %d 条, 新增 %d, 移除 %d, 现有 %d 条",
			len(channels), added, removed, store.Default.ChannelCount()))
	} else {
		global.LOG.Warn(fmt.Sprintf("频道详情异常缩水(本次解析 %d 条, 现有 %d 条), 仅更新不删除: 认证页面可能解析残缺",
			len(channels), before))
	}
}

// authSetupTwo 认证流程二：epgIndex → 负载均衡跳转 → Portal 认证。
// doc 是流程一最后一步的响应文档，只在这一步内使用，因此作为参数传入，
// 不再挂在 Client 字段上。
func (c *Client) authSetupTwo(doc *goquery.Document) (*goquery.Document, error) {
	global.LOG.Info("认证流程二")
	if doc == nil {
		return nil, errors.New("认证流程二失败: 认证流程一未生成有效文档")
	}
	doc = c.epgIndex(doc)
	if doc == nil {
		return nil, errors.New("认证流程二失败: epgIndex 未获得有效响应文档")
	}
	doc = c.epgLoadBalance(doc)
	if doc == nil {
		return nil, errors.New("认证流程二失败: epgLoadBalance 未能解析出跳转地址")
	}
	doc, err := c.epgPortalAuth(doc)
	if err != nil {
		global.LOG.Info("认证流程结束, 出现错误")
		return nil, err
	}
	c.epgGetPortal()
	global.LOG.Info("认证流程结束, 认证信息已更新")
	c.updateCookies()
	return doc, nil
}

// startAuthLocked 执行完整认证流程，调用方必须已持有 opMu。
// 成功时返回流程一解析到的频道快照（由调用方决定何时落库）。
//
// 认证过程中会分步写入 JSESSIONID / EPGHostUrl 等会话字段，因此只有
// 两步流程全部成功才返回 nil——中途失败时调用方直接跳过本轮，不会
// 拿着半成品会话去拉数据。
func (c *Client) startAuthLocked() ([]model.Channel, error) {
	global.LOG.Info("开始认证流程")
	doc, channels, err := c.authSetupOne()
	if err != nil {
		global.LOG.Error("认证流程一失败: " + err.Error())
		return nil, err
	}
	if _, err := c.authSetupTwo(doc); err != nil {
		return nil, err
	}
	return channels, nil
}

func (c *Client) updateCookies() {
	u, err := url.Parse(c.AuthInfo.EPGHostUrl)
	if err != nil {
		global.LOG.Error("更新认证信息至运行环境失败 ", zap.Any("Msg", err.Error()))
		return
	}
	c.httpClient.SetCookies(&http.Cookie{
		Name:     "JSESSIONID",
		Value:    c.AuthInfo.JSESSIONID,
		Path:     "/",
		Domain:   u.Host,
		Secure:   false,
		HttpOnly: true,
	})
}

func NewClient(uid, sn, mac, ip string) (*Client, error) {
	// 验证参数
	if !utils.CheckUserID(uid) {
		return nil, ErrUserID
	}
	if !utils.CheckSNCode(sn) {
		return nil, ErrSNCode
	}
	if !utils.CheckMacAddressV1(mac) {
		return nil, ErrMacAddr
	}
	if !utils.CheckIPv4Address(ip) {
		return nil, ErrIPAddr
	}

	// 认证服务器地址与机顶盒型号均从配置读取，服务器地址变更时
	// 改 config.yaml 即可，无需重新编译；配置为空时回退内置默认值。
	c := &Client{
		jsVM: jsvm.New(),
		baseConfig: baseConfig{
			userID:           uid,
			sn:               sn,
			macAddr:          mac,
			ip:               ip,
			userAgent:        "webkit;Resolution(PAL,720P,1080P,2106P,4K)",
			pre4kLogAuthAddr: NormalizeHostPort(global.CONFIG.Stb.AuthHost, "7001", Default4kLogAuthAddr),
			stbType:          strings.TrimSpace(global.CONFIG.Stb.Type),
		},
	}
	if c.stbType == "" {
		c.stbType = DefaultSTBType
	}
	c.httpClient = http_client.NewHttpClient(http_client.WithUserAgent(c.userAgent))
	// 这里不做认证：认证由第一轮任务（run）统一完成，
	// 避免启动时认证一次、紧接着整轮任务又认证一次。
	return c, nil
}

func NewGlobalClient(uid, sn, mac, ip string) (client *Client, err error) {
	if globalClient == nil {
		globalClient, err = NewClient(uid, sn, mac, ip)
		if err != nil {
			return nil, err
		}
	}
	return globalClient, nil
}

// NormalizeHostPort 归一化配置里的服务器地址：host / host:port / http://host
// 统一成 host:port；归一化后为空则返回 def。
func NormalizeHostPort(v, defPort, def string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "http://")
	v = strings.TrimPrefix(v, "https://")
	v = strings.TrimSuffix(v, "/")
	if v == "" {
		return def
	}
	if _, _, err := net.SplitHostPort(v); err != nil {
		v = net.JoinHostPort(v, defPort)
	}
	return v
}

func GetGlobalClient() *Client {
	return globalClient
}

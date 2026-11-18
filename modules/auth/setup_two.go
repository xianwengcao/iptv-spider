package auth

import (
	"encoding/hex"
	"fmt"
	"iptv-spider-sh/global"
	"iptv-spider-sh/utils"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/robertkrimen/otto"
	"go.uber.org/zap"
)

func (c *Client) epgIndex(doc *goquery.Document) *goquery.Document {
	uri, method, formMap := utils.GetFromParamByHtml(doc, "form#epgform")
	resp := c.httpClient.Request(uri, method, formMap)
	if resp == nil {
		global.LOG.Error("epgIndex: 请求无响应")
		return nil
	}
	return utils.CreateHtmlDocByBytes(uri, resp.Body())
}

func (c *Client) epgLoadBalance(doc *goquery.Document) *goquery.Document {
	var uri string
	scs := utils.GetScriptsFormHtml(doc)
	for _, sc := range scs {
		if !strings.Contains(sc, "top.document.location") {
			continue
		}
		sArr := strings.Split(sc, "\n")
		for _, s := range sArr {
			if !strings.Contains(s, "top.document.location") {
				continue
			}
			index := strings.Index(s, "'")
			last := strings.LastIndex(s, "'")
			if index < 0 || last < 0 {
				global.LOG.Error("No top.document.location")
				return nil
			}
			uri = s[index+1 : last]
		}
	}
	// 只校验跳转地址可解析；Next 请求直接用原始 uri（带 scheme），
	// 不再从中提取 host 存到 Client 上（该字段从未被读取）。
	if _, err := url.Parse(uri); err != nil {
		global.LOG.Error(err.Error())
		return nil
	}
	resp := c.httpClient.Request(uri, "GET", nil)
	if resp == nil {
		global.LOG.Error("epgLoadBalance: 请求无响应")
		return nil
	}
	return utils.CreateHtmlDocByBytes(uri, resp.Body())
}

func (c *Client) epgPortalAuth(doc *goquery.Document) (*goquery.Document, error) {
	// 重试次数，最大设为3次
	maxRetries := 3
	var lastErr error

	for retries := 0; retries < maxRetries; retries++ {
		uri, method, formMap := utils.GetFromParamByHtml(doc, "form")
		if strings.TrimSpace(uri) == "" {
			global.LOG.Error("epgPortalAuth 跳过：上一步响应里没有解析到表单 action，认证流程无法继续")
			return nil, fmt.Errorf("epgPortalAuth: 未解析到表单 action")
		}

		r := utils.RSA{}
		r.LoadPriKey(utils.GetRSAPriKey())

		token := formMap["UserToken"]
		plainData := utils.InsertStrInUserToken(token)
		if plainData == "" {
			global.LOG.Error("InsertStrInUserToken: plainData is empty, Token: " + token)
			return nil, fmt.Errorf("token is empty")
		}
		stbInfo := hex.EncodeToString(r.PriEncrypt([]byte(plainData)))
		formMap["stbtype"] = c.stbType
		formMap["stbinfo"] = strings.ToUpper(stbInfo)

		resp := c.httpClient.Request(uri, method, formMap)
		if resp == nil {
			global.LOG.Error("epgPortalAuth: 请求无响应")
			lastErr = fmt.Errorf("epgPortalAuth: 请求无响应")
			if retries < maxRetries-1 {
				global.LOG.Info("请求无响应，正在重试...", zap.Int("RetryAttempt", retries+1))
				continue
			}
			return nil, lastErr
		}
		respDoc := utils.CreateHtmlDocByBytes(uri, resp.Body())

		infoMap := c.parseEpgAuthInfo(respDoc)
		// 先落到局部变量，校验通过后才写回 Client。
		// 之前是解析完立刻写 c.JSESSIONID / c.EPGHostUrl，一旦 URL 校验
		// 失败或后续步骤出错，内存里就留下了"无效会话"，后续请求会带着
		// 坏 cookie 白白打一整轮。现在失败时内存状态不受影响。
		sessionID := infoMap["SessionID"]
		epgHostUrl := fmt.Sprintf("http://%s/iptvepg/%s", infoMap["IpPort"], infoMap["framecode"])

		// 校验 EPGHostUrl
		if !isValidEPGHostUrl(epgHostUrl) {
			global.LOG.Error("生成的EPGHostUrl格式无效: " + epgHostUrl)
			lastErr = fmt.Errorf("EPG地址格式无效: %s", epgHostUrl)

			// 如果不是最后一次尝试，打印重试信息并继续
			if retries < maxRetries-1 {
				global.LOG.Info("EPGHostUrl 格式无效，正在重试...", zap.Int("RetryAttempt", retries+1))
				continue
			}
			return nil, lastErr
		}

		// 校验通过，提交到 Client
		c.JSESSIONID = sessionID
		c.EPGHostUrl = epgHostUrl
		global.LOG.Info("EPG门户认证成功", zap.String("EPGHostUrl", c.EPGHostUrl))
		return respDoc, nil
	}

	return nil, lastErr // 如果最终没有成功，则返回最后的错误
}

// isValidEPGHostUrl 检查 EPGHostUrl 是否符合标准格式
func isValidEPGHostUrl(urlStr string) bool {
	// 尝试解析 URL 并检查是否符合预期格式
	u, err := url.Parse(urlStr)
	if err != nil {
		global.LOG.Error("EPGHostUrl 解析失败: ", zap.Any("url", urlStr))
		return false
	}

	// 检查 URL 的 Host 部分是否是有效的 IP 或域名
	if u.Host == "" || !strings.Contains(u.Host, ":") {
		return false
	}

	// 检查是否符合期望的 URL 格式，例如: http://218.83.188.230:8084/iptvepg/frame1002
	if !strings.HasPrefix(urlStr, "http://") {
		return false
	}

	return true
}

func (c *Client) parseEpgAuthInfo(doc *goquery.Document) map[string]string {
	cache := map[string]string{}
	c.jsVM.Reset()
	c.jsVM.RunScriptForHtml(doc)
	c.jsVM.Set("jsSetConfig", func(call otto.FunctionCall) otto.Value {
		k := call.Argument(0).String()
		v := call.Argument(1).String()
		cache[k] = v
		return otto.Value{}
	})
	scs := utils.GetScriptsFormHtml(doc)
	for _, sc := range scs {
		sArr := strings.Split(sc, "\n")
		for _, s := range sArr {
			if !strings.Contains(s, "jsSetConfig") {
				continue
			}
			c.jsVM.RunScript(s)
		}
	}
	// 打印解析到的配置信息
	global.LOG.Info(fmt.Sprintf("解析到的IPTV配置信息: %+v", cache))

	return cache
}

func (c *Client) epgGetPortal() {
	// http://218.83.165.40:8084/iptvepg/frame1413/portal.jsp
	p := "portal.jsp"
	uri := fmt.Sprintf("%s/%s", c.EPGHostUrl, p)
	c.httpClient.Request(uri, "GET", nil)
}

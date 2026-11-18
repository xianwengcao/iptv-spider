package http_client

import (
	"fmt"
	"github.com/go-resty/resty/v2"
	"iptv-spider-sh/global"
	"net/http"
	"strings"
	"time"
)

// requestTimeout 单次 HTTP 请求的整体超时（连接 + 读取响应）。
const requestTimeout = 20 * time.Second

type HttpClient struct {
	client    *resty.Client
	userAgent string
}

// Request 执行一次 HTTP 请求并返回本次请求的响应。
//
// 并发安全说明：返回值是调用方独享的 *resty.Response，而不是共享的
// HttpClient 自身——resty.Client 本身可并发使用，之前把响应挂在
// HttpClient 的字段上会导致多个 goroutine 同时请求时互相覆盖响应体
// （A 频道解析到 B 频道的数据），因此必须在返回值中传递本次结果。
func (c *HttpClient) Request(uri, method string, form map[string]string) *resty.Response {
	r := c.client.R()
	method = strings.ToUpper(method)
	switch method {
	case "GET":
		r.SetQueryParams(form)
	case "POST":
		r.SetFormData(form)
	}
	global.LOG.Debug(fmt.Sprintf("%s %s", method, uri))
	resp, err := r.Execute(method, uri)
	if err != nil {
		global.LOG.Error(fmt.Sprintf("%s %s", method, uri))
		global.LOG.Error(err.Error())
		// 出站网卡绑定已启用时，补一条排查提示：
		// 认证接口的 i/o timeout / 网络不可达，绝大多数是 system.dev
		// 指到了到不了专网的网卡（或该网卡没有可用 IPv4 / 路由）。
		if b := global.BIND.Default; b.Enabled() {
			global.LOG.Error("出站网卡绑定已启用 -> " + b.Describe())
			global.LOG.Error("若上述错误是 i/o timeout / no route to host，请先执行 `iptv-spider-sh -netcheck` " +
				"找出能连通专网的网卡；或临时把 config.yaml 里的 system.dev 置空后重启对比")
		}
	}
	if resp != nil {
		global.LOG.Debug(fmt.Sprintf("Resp Body: %s", string(resp.Body())))
	}
	return resp
}

func NewHttpClient(opts ...HttpClientOption) *HttpClient {
	c := &HttpClient{
		userAgent: "webkit;Resolution(PAL,720P,1080P,2106P,4K)",
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.client == nil {
		c.client = resty.New()
		// 出站网卡绑定：让 IPTV 认证、EPG、频道列表等请求
		// 从 config 中 system.dev 指定的网络接口发出。
		// 未配置 dev 时 Transport() 返回 nil，行为与改动前完全一致。
		c.client.SetTransport(global.BIND.Default.Transport())
	}
	// 必须设超时：resty 默认永不超时，一次 TCP 挂起（专网抖动、对端不响应）
	// 就会长期占住认证/拉取的锁，把整轮任务和后续轮次一起卡死。
	// 20s 对认证（3 跳）与单个频道节目单（通常 <1s）都足够宽松。
	c.client.SetTimeout(requestTimeout)
	c.afterAction()
	return c
}

func (c *HttpClient) afterAction() {
	// setUserAgent
	c.client.SetHeader("User-Agent", c.userAgent)
}

func (c *HttpClient) SetCookies(cookies ...*http.Cookie) {
	if len(cookies) <= 0 {
		return
	}
	c.client.SetCookies(cookies)
}

type HttpClientOption func(client *HttpClient)

func WithUserAgent(ua string) HttpClientOption {
	return func(c *HttpClient) {
		c.userAgent = ua
	}
}

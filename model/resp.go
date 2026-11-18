package model

import (
	"fmt"
	"strings"
)

// JsonResponse 电信 EPG 接口的统一响应外壳。
//
// 只声明真正会被读取的字段：status 实测恒为 "1"（正常返回 169 条频道时
// 也是 "1"），从不构成失败标志，已删除；JSON 里多余的键在反序列化时
// 会被自动忽略。
type JsonResponse[T ChannelInfo | EPGDetails] struct {
	ErrCode string `json:"errCode"`
	ErrMsg  string `json:"errMsg"`
	Data    []T    `json:"data"`
}

// noDataCodes 服务端用业务错误码表达"这个频道确实没有节目单"。
// 这类返回不是故障：频道可能是新上线、试播、或只在特定时段提供节目。
// 单独列出来是为了把它们计入"无数据"而不是"失败"——否则每轮都会多出
// 一条假失败，掩盖真正的请求错误。
//
//	051002 根据频道ID[x], 未获取到节目单数据
var noDataCodes = map[string]struct{}{
	"051002": {},
}

// ErrInfo 返回接口层的错误描述，空字符串表示接口调用正常。
//
// 电信 EPG 接口在参数错误、权限不足、会话异常等情况下**仍然返回 HTTP 200**，
// 错误只体现在 errCode / errMsg 上。之前这两个字段从未被检查，服务端的业务
// 错误会被一律当成"该频道没有数据"，日志里只能看到"无数据 1"而看不到原因。
// 业务错误一律带 errCode，因此只认它。
func (r JsonResponse[T]) ErrInfo() string {
	errCode := strings.TrimSpace(r.ErrCode)
	if errCode == "" || errCode == "0" {
		return ""
	}
	return fmt.Sprintf("errCode=%s errMsg=%s", r.ErrCode, r.ErrMsg)
}

// IsNoData 判断本次业务错误是否属于"该频道确实没有数据"，
// 而不是参数、权限、会话之类的故障。
func (r JsonResponse[T]) IsNoData() bool {
	_, ok := noDataCodes[strings.TrimSpace(r.ErrCode)]
	return ok
}

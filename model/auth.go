package model

// AuthInfo 一轮拉取所需的会话信息。
//
// 会话只在"认证 → 拉取"这一轮内有效，不跨轮保存，因此只保留真正会被
// 后续请求用到的两个字段：EPG 主机地址与 JSESSIONID。
// UID / UserToken / EPGLoginHost / R4kLoginHost 过去只在认证中途赋值、
// 全项目从不读取，已删除（UID 用 Client.userID，Token 只作即时计算输入）。
type AuthInfo struct {
	JSESSIONID string
	EPGHostUrl string
}

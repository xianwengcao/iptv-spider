package model

// Channel 频道播放详情，来源于认证页面里的 channelArray。
//
// 只保留会参与"拼播放地址"的字段。channelArray 里的 ChannelID / TimeShift /
// ChannelSDP / ChannelType 曾在反射绑定时被解析进来，但全项目从不读取，
// 已删除；CreatedAt / UpdatedAt 也一并去掉——这张表每次认证都是整体替换，
// 不按时间戳做任何判断。
type Channel struct {
	UserChannelID  string `key:"UserChannelID"`
	ChannelURL     string `key:"ChannelURL"`
	TimeShiftURL   string `key:"TimeShiftURL"`
	ChannelFCCPort string `key:"ChannelFCCPort"`
	ChannelFCCIP   string `key:"ChannelFCCIP"`
}

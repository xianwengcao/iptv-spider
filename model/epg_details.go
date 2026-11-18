package model

// EPGDetails EPG 接口响应的完整解析结构（仅用于解析 JSON，含大量入库后不再读取的字段）。
type EPGDetails struct {
	CommName          string `json:"-"`
	InterRecordStatus string `json:"interRecordStatus"`
	Name              string `json:"name"`
	ChannelName       string `json:"channelName"`
	StartTime         int64  `json:"startTime"`
	ID                string `json:"ID"`
	EndTime           int64  `json:"endTime"`
	MixNo             string `json:"mixNo"`
	ChannelID         string `json:"channelID"`
	Status            string `json:"status"`
}

// EPGItem 内存存储使用的瘦身节目条目。
// 抓取阶段用 EPGDetails 完整解析接口响应，入库时仅保留
// 生成 m3u/XMLTV 时实际会读取的 3 个字段（Name/StartTime/EndTime），
// 相比完整结构可节省约一半的节目单内存占用。
// ID 仅在入库去重阶段使用，不落存储。
type EPGItem struct {
	Name      string
	StartTime int64
	EndTime   int64
}

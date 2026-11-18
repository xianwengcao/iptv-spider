package model

import "encoding/xml"

// 以下类型只服务于 golden 测试：用 encoding/xml 的正向序列化结果去校验
// XmlTvStreamWriter 的逐字节输出。生产代码走流式写出，不再需要这些结构体，
// 因此它们只存在于测试包内。

// XmlTV tv tag
type XmlTV struct {
	Generator string   `xml:"generator-info-name,attr"`
	Source    string   `xml:"source-info-name,attr"`
	XMLName   xml.Name `xml:"tv"`

	Channel []*XmlTvChannel `xml:"channel"`
	Program []*Program      `xml:"programme"`
}

// XmlTvChannel : channel info
type XmlTvChannel struct {
	ID          string        `xml:"id,attr"`
	DisplayName []DisplayName `xml:"display-name"`
}

// DisplayName 频道名
type DisplayName struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

// Program 节目
type Program struct {
	Channel string `xml:"channel,attr"`
	Start   string `xml:"start,attr"`
	Stop    string `xml:"stop,attr"`

	Title []*Title `xml:"title"`
	Desc  []*Desc  `xml:"desc"`
}

// Title 节目标题
type Title struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

// Desc : 节目描述
type Desc struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

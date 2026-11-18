package model

import (
	"bufio"
	"encoding/xml"
	"io"
)

// XmlTvStreamWriter 流式写出 XMLTV 文档，输出与 encoding/xml 的
// MarshalIndent 结果逐字节一致（基准结构体见 xmltv_golden_test.go）。
//
// 相比先构建整棵文档树再序列化，流式写出省去了数万个
// Program/Title/Desc 对象及其字符串的中间分配，
// 生成时间与峰值内存都大幅下降。
type XmlTvStreamWriter struct {
	w *bufio.Writer
	// children 记录已写出的子元素数量，用于在没有任何子元素时
	// 输出单行空元素（与 encoding/xml 的行为一致）
	children int
}

// NewXmlTvStreamWriter 创建流式 XMLTV 写入器。
func NewXmlTvStreamWriter(w io.Writer) *XmlTvStreamWriter {
	return &XmlTvStreamWriter{w: bufio.NewWriter(w)}
}

// escape 按 encoding/xml 的规则转义并写出文本。
func (x *XmlTvStreamWriter) escape(s string) error {
	return xml.EscapeText(x.w, []byte(s))
}

// Header 写出 XML 声明与 <tv> 根元素开始标签。
func (x *XmlTvStreamWriter) Header(generator, source string) error {
	if _, err := x.w.WriteString(PrefixHeader + "\n<tv generator-info-name=\""); err != nil {
		return err
	}
	if err := x.escape(generator); err != nil {
		return err
	}
	if _, err := x.w.WriteString("\" source-info-name=\""); err != nil {
		return err
	}
	if err := x.escape(source); err != nil {
		return err
	}
	_, err := x.w.WriteString("\">")
	return err
}

// Channel 写出一个 <channel> 元素。
func (x *XmlTvStreamWriter) Channel(id, displayName string) error {
	x.children++
	if _, err := x.w.WriteString("\n  <channel id=\""); err != nil {
		return err
	}
	if err := x.escape(id); err != nil {
		return err
	}
	if _, err := x.w.WriteString("\">\n    <display-name lang=\"zh\">"); err != nil {
		return err
	}
	if err := x.escape(displayName); err != nil {
		return err
	}
	_, err := x.w.WriteString("</display-name>\n  </channel>")
	return err
}

// Programme 写出一个 <programme> 元素（标题为空时输出空 title，与原有结构一致）。
func (x *XmlTvStreamWriter) Programme(channel, start, stop, title string) error {
	x.children++
	if _, err := x.w.WriteString("\n  <programme channel=\""); err != nil {
		return err
	}
	if err := x.escape(channel); err != nil {
		return err
	}
	if _, err := x.w.WriteString("\" start=\""); err != nil {
		return err
	}
	if err := x.escape(start); err != nil {
		return err
	}
	if _, err := x.w.WriteString("\" stop=\""); err != nil {
		return err
	}
	if err := x.escape(stop); err != nil {
		return err
	}
	if _, err := x.w.WriteString("\">\n    <title lang=\"zh\">"); err != nil {
		return err
	}
	if err := x.escape(title); err != nil {
		return err
	}
	_, err := x.w.WriteString("</title>\n    <desc lang=\"zh\"></desc>\n  </programme>")
	return err
}

// Footer 写出根元素结束标签并刷新缓冲。
func (x *XmlTvStreamWriter) Footer() error {
	if x.children == 0 {
		if _, err := x.w.WriteString("</tv>"); err != nil {
			return err
		}
	} else if _, err := x.w.WriteString("\n</tv>"); err != nil {
		return err
	}
	return x.w.Flush()
}

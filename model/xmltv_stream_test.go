package model

import (
	"bytes"
	"encoding/xml"
	"testing"
)

// buildSample 构造一份包含特殊字符的样例，用于对比流式输出与 encoding/xml 的输出。
func buildSample() *XmlTV {
	return &XmlTV{
		XMLName:   xml.Name{Local: "tv"},
		Generator: "Deny 2026-11-16 12:00:00 <&\"'>",
		Source:    "上海电信 & IPTV",
		Channel: []*XmlTvChannel{
			{ID: "1", DisplayName: []DisplayName{{Lang: "zh", Value: "CCTV-1&综合<高清>"}}},
			{ID: "2", DisplayName: []DisplayName{{Lang: "zh", Value: "东方卫视"}}},
		},
		Program: []*Program{
			{
				Channel: "1",
				Start:   "20261116080000 +0800",
				Stop:    "20261116090000 +0800",
				Title:   []*Title{{Lang: "zh", Value: `新闻 "联播" & <要闻>`}},
				Desc:    []*Desc{{Lang: "zh"}},
			},
			{
				// 空节目（无标题与起止时间）的写法
				Channel: "2",
				Title:   []*Title{{Lang: "zh"}},
				Desc:    []*Desc{{Lang: "zh"}},
			},
			{
				Channel: "2",
				Start:   "20261116233000 +0800",
				Stop:    "20261117003000 +0800",
				Title:   []*Title{{Lang: "zh", Value: "午夜剧场"}},
				Desc:    []*Desc{{Lang: "zh"}},
			},
		},
	}
}

// TestXmlTvStreamMatchesMarshalIndent 保证流式写出与原来的
// xml.MarshalIndent(&XmlTV, "", "  ") 输出逐字节一致（含 XML 声明与转义规则）。
func TestXmlTvStreamMatchesMarshalIndent(t *testing.T) {
	tv := buildSample()

	want, err := xml.MarshalIndent(tv, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want = append([]byte(PrefixHeader+"\n"), want...)

	var buf bytes.Buffer
	sw := NewXmlTvStreamWriter(&buf)
	if err := sw.Header(tv.Generator, tv.Source); err != nil {
		t.Fatal(err)
	}
	for _, ch := range tv.Channel {
		if err := sw.Channel(ch.ID, ch.DisplayName[0].Value); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range tv.Program {
		title := ""
		if len(p.Title) > 0 {
			title = p.Title[0].Value
		}
		if err := sw.Programme(p.Channel, p.Start, p.Stop, title); err != nil {
			t.Fatal(err)
		}
	}
	if err := sw.Footer(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("流式输出与 MarshalIndent 不一致\n--- want ---\n%s\n--- got ---\n%s", want, buf.String())
	}
}

// TestXmlTvStreamEmpty 空文档（无任何频道/节目）也要与 encoding/xml 一致。
func TestXmlTvStreamEmpty(t *testing.T) {
	tv := &XmlTV{
		XMLName:   xml.Name{Local: "tv"},
		Generator: "G",
		Source:    "S",
	}
	want, err := xml.MarshalIndent(tv, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want = append([]byte(PrefixHeader+"\n"), want...)

	var buf bytes.Buffer
	sw := NewXmlTvStreamWriter(&buf)
	if err := sw.Header(tv.Generator, tv.Source); err != nil {
		t.Fatal(err)
	}
	if err := sw.Footer(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("空文档不一致\n--- want ---\n%s\n--- got ---\n%s", want, buf.String())
	}
}

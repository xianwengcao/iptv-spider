package auth

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"iptv-spider-sh/config"
	"iptv-spider-sh/global"
	"iptv-spider-sh/model"
	"iptv-spider-sh/store"
)

// generate 造一个频道 + n 条同标题节目，跑一遍 GenerateXmlTv
func generate(t *testing.T, title string, n int) []byte {
	t.Helper()

	global.LOG = zap.NewNop()
	global.CONFIG = &config.Server{}
	global.CONFIG.Epg.Generator = "test"
	global.CONFIG.Epg.Source = "test"

	// Name 以 HD 结尾 → CommName 归一为 CCTV-1
	store.Default.UpsertChannelInfos([]model.ChannelInfo{
		{Code: "c1", Name: "CCTV-1HD", ChID: "100", MixNo: "1"},
	}, nil)

	now := time.Now().UnixMilli()
	epgs := make([]model.EPGDetails, 0, n)
	for i := 0; i < n; i++ {
		epgs = append(epgs, model.EPGDetails{
			ID:        fmt.Sprintf("e%d", i),
			Name:      title,
			StartTime: now + int64(i)*60000,
			EndTime:   now + int64(i+1)*60000,
		})
	}
	store.Default.ReplaceEPG("CCTV-1", epgs)

	out, err := GenerateXmlTv(1)
	if err != nil {
		t.Fatalf("GenerateXmlTv 失败: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("生成结果为空")
	}
	if got := strings.Count(string(out), "<programme"); got != n {
		t.Fatalf("programme 条数不符，期望 %d 实际 %d", n, got)
	}
	return out
}

// 缓存会长期持有生成的字节，而切片指针引用的是整块底层数组、GC 无法只回收
// 未使用的尾部，所以 cap 必须贴近 len：预统计准确时直接返回不复制，
// 转义膨胀超出 5% 余量时才走兜底复制。
func TestGenerateXmlTvSliceCapacityTight(t *testing.T) {
	cases := []struct {
		name  string
		title string
	}{
		{"普通中文标题", "新闻联播"},
		{"转义字符密集", `A&B<C>"D"E&F`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := generate(t, tc.title, 800)
			if cap(out) > len(out)+len(out)/8 {
				t.Fatalf("cap 超出 len 的 12.5%%：len=%d cap=%d（多出 %d 字节无法回收）",
					len(out), cap(out), cap(out)-len(out))
			}
		})
	}
}

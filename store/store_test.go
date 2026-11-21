package store

import (
	"sync"
	"testing"
	"time"

	"iptv-spider-sh/model"
)

func TestVersionBumpsOnWrite(t *testing.T) {
	s := New()
	v0 := s.Version()

	s.ReplaceChannels([]model.Channel{{UserChannelID: "1", ChannelURL: "igmp://1.1.1.1:5140"}})
	if s.Version() <= v0 {
		t.Fatal("ReplaceChannels 应递增版本号")
	}

	v1 := s.Version()
	s.UpsertChannelInfos([]model.ChannelInfo{{MixNo: "10", Name: "CCTV-1HD"}}, nil)
	if s.Version() <= v1 {
		t.Fatal("UpsertChannelInfos 应递增版本号")
	}
}

// 同一个 MixNo 再次写入时整体覆盖（以接口返回为准），且不产生重复条目。
func TestUpsertChannelInfoOverwrites(t *testing.T) {
	s := New()
	s.UpsertChannelInfos([]model.ChannelInfo{{MixNo: "10", Name: "CCTV-1HD", Code: "c1", ChID: "100"}}, nil)
	s.UpsertChannelInfos([]model.ChannelInfo{{MixNo: "10", Name: "CCTV-1HD", Code: "c2", ChID: "200"}}, nil)

	got := s.ListChannelInfos()
	if len(got) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(got))
	}
	if got[0].Code != "c2" || got[0].ChID != "200" {
		t.Fatalf("应以最后一次写入为准，实际 Code=%s ChID=%s", got[0].Code, got[0].ChID)
	}
	if got[0].UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt 应被写入")
	}
	if got[0].CommName != "CCTV-1" {
		t.Fatalf("CommName 应为 CCTV-1，实际 %s", got[0].CommName)
	}
}

func TestReplaceEPGAndQuery(t *testing.T) {
	s := New()
	base := time.Now().UnixMilli()
	s.ReplaceEPG("CCTV-1", []model.EPGDetails{
		{ID: "p1", Name: "新闻", StartTime: base, EndTime: base + 1000},
		{ID: "p2", Name: "天气", StartTime: base + 1000, EndTime: base + 2000},
		{ID: "p2", Name: "天气B", StartTime: base + 1000, EndTime: base + 2500}, // ID 重复，后者覆盖
	})
	res := s.QueryEPG([]string{"CCTV-1"}, 0)
	list := res["CCTV-1"]
	if len(list) != 2 {
		t.Fatalf("ID 去重后应剩 2 条，实际 %d", len(list))
	}
	if list[0].Name != "新闻" {
		t.Fatal("应按 StartTime 排序，p1 在前")
	}
	if list[1].Name != "天气B" {
		t.Fatal("重复 ID 应保留后写入的记录")
	}

	// 整组替换
	s.ReplaceEPG("CCTV-1", []model.EPGDetails{
		{ID: "p3", Name: "新节目", StartTime: base, EndTime: base + 1000},
	})
	res = s.QueryEPG([]string{"CCTV-1"}, 0)
	if len(res["CCTV-1"]) != 1 || res["CCTV-1"][0].Name != "新节目" {
		t.Fatal("ReplaceEPG 应整组替换")
	}
}

func TestCleanExpired(t *testing.T) {
	s := New()
	base := time.Now().UnixMilli()
	s.ReplaceEPG("CCTV-1", []model.EPGDetails{
		{ID: "p1", StartTime: base, EndTime: base - 1},    // 过期
		{ID: "p2", StartTime: base, EndTime: base + 1000}, // 有效
	})
	if n := s.CleanEPGBefore(base); n != 1 {
		t.Fatalf("应清理 1 条，实际 %d", n)
	}
	if s.EPGCount() != 1 {
		t.Fatal("清理后应剩 1 条")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); s.UpsertChannelInfos([]model.ChannelInfo{{MixNo: "1", Name: "X"}}, nil) }()
		go func() { defer wg.Done(); s.ListChannelInfos() }()
		go func() { defer wg.Done(); s.Version() }()
	}
	wg.Wait()
}

func TestBeginBatchBumpsOnce(t *testing.T) {
	s := New()
	base := time.Now().UnixMilli()
	v0 := s.Version()

	// 模拟一轮拉取：多个频道反复写入
	s.BeginBatch()
	for i := 0; i < 10; i++ {
		s.ReplaceEPG("CCTV-1", []model.EPGDetails{
			{ID: "p1", Name: "新闻", StartTime: base, EndTime: base + 1000},
		})
		s.UpsertChannelInfos([]model.ChannelInfo{{MixNo: "10", Name: "CCTV-1HD"}}, nil)
	}
	if got := s.Version(); got != v0 {
		t.Fatalf("批量区间内版本号不应变化，期望 %d，实际 %d", v0, got)
	}
	s.EndBatch()
	if got := s.Version(); got != v0+1 {
		t.Fatalf("EndBatch 应只递增一次，期望 %d，实际 %d", v0+1, got)
	}
	// 数据本身必须已经落库
	if s.EPGCount() != 1 {
		t.Fatal("批量区间内的写入应正常生效")
	}
}

func TestEndBatchWithoutWriteKeepsVersion(t *testing.T) {
	s := New()
	v0 := s.Version()
	s.BeginBatch()
	s.EndBatch()
	if got := s.Version(); got != v0 {
		t.Fatalf("区间内无写入时不应递增版本号，实际 %d", got)
	}
}

func TestNestedBatch(t *testing.T) {
	s := New()
	v0 := s.Version()
	s.BeginBatch()
	s.BeginBatch()
	s.ReplaceChannels([]model.Channel{{UserChannelID: "1", ChannelURL: "igmp://1.1.1.1:5140"}})
	s.EndBatch()
	if got := s.Version(); got != v0 {
		t.Fatalf("内层 EndBatch 不应递增版本号，实际 %d", got)
	}
	s.EndBatch()
	if got := s.Version(); got != v0+1 {
		t.Fatalf("最外层 EndBatch 应递增版本号，期望 %d，实际 %d", v0+1, got)
	}
}

// 空列表不代表数据变化：不应递增版本号，否则会让所有 API 缓存无故失效。
func TestEmptyListDoesNotBumpVersion(t *testing.T) {
	s := New()
	v0 := s.Version()

	s.ReplaceChannels(nil)
	s.UpsertChannelInfos([]model.ChannelInfo{}, nil)

	if got := s.Version(); got != v0 {
		t.Fatalf("无实际写入时版本号不应变化，期望 %d，实际 %d", v0, got)
	}
}

// 认证时的快照同步：新集合里没有的旧频道应被剔除。
func TestReplaceChannelsRemovesStale(t *testing.T) {
	s := New()
	s.ReplaceChannels([]model.Channel{
		{UserChannelID: "1", ChannelURL: "igmp://1.1.1.1:5140"},
		{UserChannelID: "2", ChannelURL: "igmp://2.2.2.2:5140"},
		{UserChannelID: "3", ChannelURL: "igmp://3.3.3.3:5140"},
		{UserChannelID: "4", ChannelURL: "igmp://4.4.4.4:5140"},
		{UserChannelID: "5", ChannelURL: "igmp://5.5.5.5:5140"},
	})
	if got := s.ChannelCount(); got != 5 {
		t.Fatalf("初始应有 5 条频道详情，实际 %d", got)
	}

	// 频道 5 下线（新快照里没有）。4/5=80% 不低于 70% 阈值，不触发缩水保护。
	added, removed, synced := s.ReplaceChannels([]model.Channel{
		{UserChannelID: "1", ChannelURL: "igmp://1.1.1.1:5140"},
		{UserChannelID: "2", ChannelURL: "igmp://2.2.2.2:5140"},
		{UserChannelID: "3", ChannelURL: "igmp://3.3.3.3:5140"},
		{UserChannelID: "4", ChannelURL: "igmp://4.4.4.4:5140"},
	})
	if !synced || removed != 1 || added != 0 {
		t.Fatalf("期望同步并移除 1 条，实际 synced=%v added=%d removed=%d", synced, added, removed)
	}
	if got := s.ChannelCount(); got != 4 {
		t.Fatalf("剔除后应剩 4 条，实际 %d", got)
	}
	if _, ok := s.GetChannelByUserChannelID("5"); ok {
		t.Fatal("已下线的频道 5 不应再存在")
	}

	// 新增频道
	added, removed, _ = s.ReplaceChannels([]model.Channel{
		{UserChannelID: "1"}, {UserChannelID: "2"}, {UserChannelID: "3"},
		{UserChannelID: "4"}, {UserChannelID: "6"},
	})
	if added != 1 || removed != 0 {
		t.Fatalf("期望新增 1 条且不删除，实际 added=%d removed=%d", added, removed)
	}
}

// 解析残缺保护：新集合相对现有集合显著缩水时，只更新不删除。
func TestReplaceChannelsShrinkGuard(t *testing.T) {
	s := New()
	var full []model.Channel
	for _, id := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"} {
		full = append(full, model.Channel{UserChannelID: id})
	}
	s.ReplaceChannels(full)

	// 只解析到 3 条（30%），远低于 70% 阈值 → 不得删除
	added, removed, synced := s.ReplaceChannels([]model.Channel{
		{UserChannelID: "1"}, {UserChannelID: "2"}, {UserChannelID: "3"},
	})
	if synced || removed != 0 {
		t.Fatalf("缩水时应只更新不删除，实际 synced=%v removed=%d", synced, removed)
	}
	if added != 0 {
		t.Fatalf("三条均已存在，不应有新增，实际 %d", added)
	}
	if got := s.ChannelCount(); got != 10 {
		t.Fatalf("保护生效后仍应保留 10 条，实际 %d", got)
	}
}

// 频道集合不变、但播放地址变了（运营商迁移组播地址）时，版本号必须前进：
// 否则 m3u 的缓存 key 不变，下游会一直拿到旧地址那份缓存。
func TestReplaceChannelsBumpsVersionOnURLChange(t *testing.T) {
	s := New()
	s.ReplaceChannels([]model.Channel{{UserChannelID: "1", ChannelURL: "igmp://1.1.1.1:5140"}})
	v0 := s.Version()

	// 完全相同的快照：不增不删、地址未变 → 版本号不应变化
	added, removed, _ := s.ReplaceChannels([]model.Channel{{UserChannelID: "1", ChannelURL: "igmp://1.1.1.1:5140"}})
	if added != 0 || removed != 0 {
		t.Fatalf("相同快照不应产生增删，实际 added=%d removed=%d", added, removed)
	}
	if v := s.Version(); v != v0 {
		t.Fatalf("相同快照不应改变版本号，期望 %d，实际 %d", v0, v)
	}

	// 仅播放地址变化 → 版本号必须前进
	s.ReplaceChannels([]model.Channel{{UserChannelID: "1", ChannelURL: "igmp://9.9.9.9:5140"}})
	if v := s.Version(); v <= v0 {
		t.Fatalf("播放地址变化后版本号应前进，原 %d，实际 %d", v0, v)
	}
}

// 空集合不改变任何数据（避免认证页解析彻底失败时清空频道表）。
func TestReplaceChannelsEmptyIsNoop(t *testing.T) {
	s := New()
	s.ReplaceChannels([]model.Channel{{UserChannelID: "1"}})
	v0 := s.Version()

	added, removed, synced := s.ReplaceChannels(nil)
	if added != 0 || removed != 0 || synced {
		t.Fatalf("空集合应为无操作，实际 added=%d removed=%d synced=%v", added, removed, synced)
	}
	if s.ChannelCount() != 1 || s.Version() != v0 {
		t.Fatal("空集合不应改变数据或版本号")
	}
}

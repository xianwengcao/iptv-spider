// Package store 提供进程内存储，替代原先的 MySQL/Gorm 持久层。
//
// 数据特点：频道列表、节目单都是可随时重新抓取的缓存数据，
// 没有必须跨进程保留的内容，因此全部保存在内存中即可。
// 所有方法都是并发安全的（读写锁保护）。
package store

import (
	"sort"
	"strings"
	"sync"
	"time"

	"iptv-spider-sh/model"
)

// Default 进程级默认存储实例。
// 注意：不能把 store 实例挂到 global 包上——store 依赖 model，
// 而 model → utils → global，global 再引用 store 会形成导入环。
var Default = New()

// Store 内存存储。
type Store struct {
	mu sync.RWMutex

	// version 数据版本号：任何写操作都会递增。
	// 用于 API 响应缓存的自动失效——缓存 key 拼接版本号，
	// 数据一旦更新，旧版本的缓存条目即刻作废（等待 TTL 回收即可）。
	version uint64

	// batchDepth > 0 表示处于批量写入区间（见 BeginBatch），
	// 期间写操作只置 batchDirty，不递增 version；
	// 最外层 EndBatch 时统一递增一次。
	batchDepth int
	batchDirty bool

	// key: UserChannelID
	channels map[string]model.Channel
	// key: MixNo
	chanInfos map[string]model.ChannelInfo
	// key: CommName，值使用瘦身结构（见 model.EPGItem）
	epg map[string][]model.EPGItem
	// key: CommName
	mappings map[string]model.M3u8Mapping
}

func New() *Store {
	return &Store{
		channels:  make(map[string]model.Channel),
		chanInfos: make(map[string]model.ChannelInfo),
		epg:       make(map[string][]model.EPGItem),
		mappings:  make(map[string]model.M3u8Mapping),
	}
}

// bumpVersion 递增数据版本号（必须在持有写锁时调用）。
// 批量写入区间内只做标记，由 EndBatch 统一递增。
func (s *Store) bumpVersion() {
	if s.batchDepth > 0 {
		s.batchDirty = true
		return
	}
	s.version++
}

// BeginBatch 开启批量写入区间：区间内所有写操作都不递增版本号，
// 直到最外层 EndBatch 才统一递增一次。
//
// 用途：一轮拉取会写 100+ 次（频道快照 + 频道列表 + 每个频道一次 ReplaceEPG），
// 若每次写都递增版本号，缓存 key 在拉取的十几秒内会连续变化上百次，
// 下游密集轮询时会触发大量重复生成。批量写入后：
//   - 拉取期间版本号不变 → 下游继续命中旧缓存（拿到的是上一轮的完整数据）；
//   - 拉取结束版本号 +1 → 缓存一次性切换到新数据。
//
// 必须与 EndBatch 成对使用（建议 defer），且两者都要在写锁之外调用。
func (s *Store) BeginBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchDepth++
}

// EndBatch 结束批量写入区间。最外层结束时，若区间内有过写入，版本号 +1。
func (s *Store) EndBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batchDepth > 0 {
		s.batchDepth--
	}
	if s.batchDepth == 0 && s.batchDirty {
		s.batchDirty = false
		s.version++
	}
}

// Version 返回当前数据版本号。
// 每次数据写入（频道列表、节目单、清理等）都会递增；
// API 缓存以它作为 key 的一部分，实现数据更新后缓存自动失效。
// 批量写入区间内（BeginBatch ~ EndBatch）保持不变，区间结束时统一递增一次。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// ---------------------------------------------------------------- 频道播放详情 Channel

// replaceShrinkGuardRatio 频道集合"缩水保护"比例（分母 10）：新集合不足旧集合的
// 70% 时，视为认证页面解析残缺，只更新不删除。
const replaceShrinkGuardRatio = 7

// ReplaceChannels 用最新一次认证解析到的频道集合整体替换频道播放详情。
//
// 为什么是"替换"而不是"累加"：频道详情只在认证流程里产生，本质是认证页面的一份
// 快照。认证后仍留在内存、但新快照里已经没有的条目，就是运营商侧已下线的频道。
// 快照替换是唯一能准确判断"下线"的时机——按时间戳猜需要每个条目都有可靠的
// 更新时间，而快照本身就是一次性的，没有这种语义。
//
// 返回的 synced 表示本次是否执行了删除（集合是否已与快照对齐）。新集合为空、
// 或相对现有集合显著缩水时只做更新、不删除，避免解析残缺把仍在使用的频道误删。
func (s *Store) ReplaceChannels(list []model.Channel) (added, removed int, synced bool) {
	keep := make(map[string]struct{}, len(list))
	for _, ch := range list {
		if ch.UserChannelID != "" {
			keep[ch.UserChannelID] = struct{}{}
		}
	}
	if len(keep) == 0 {
		return 0, 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 缩水保护：现有集合为空时无需判断；否则新集合不得低于现有集合的 70%
	allowDelete := len(s.channels) == 0 || len(keep)*10 >= len(s.channels)*replaceShrinkGuardRatio

	changed := false
	for _, ch := range list {
		if ch.UserChannelID == "" {
			continue
		}
		if old, ok := s.channels[ch.UserChannelID]; ok {
			// 频道集合没变、但播放参数变了（运营商迁移组播地址、换 FCC 节点），
			// 也必须让版本号前进：否则 m3u 的缓存 key 不变，下游会一直拿到
			// 旧地址那份缓存，直到 TTL 兜底过期。
			if old.ChannelURL != ch.ChannelURL ||
				old.ChannelFCCIP != ch.ChannelFCCIP ||
				old.ChannelFCCPort != ch.ChannelFCCPort ||
				old.TimeShiftURL != ch.TimeShiftURL {
				changed = true
			}
		} else {
			added++
		}
		s.channels[ch.UserChannelID] = ch
	}
	if allowDelete {
		for k := range s.channels {
			if _, in := keep[k]; !in {
				delete(s.channels, k)
				removed++
			}
		}
	}
	if added > 0 || removed > 0 || changed {
		s.bumpVersion()
	}
	return added, removed, allowDelete
}

// GetChannelByUserChannelID 按 UserChannelID 查询频道播放详情。
func (s *Store) GetChannelByUserChannelID(id string) (model.Channel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ch, ok := s.channels[id]
	return ch, ok
}

// GetChannelByURL 按 ChannelURL（如 igmp://...）查询频道播放详情。
func (s *Store) GetChannelByURL(u string) (model.Channel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.channels {
		if ch.ChannelURL == u {
			return ch, true
		}
	}
	return model.Channel{}, false
}

// ChannelCount 返回频道播放详情条数。
func (s *Store) ChannelCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.channels)
}

// ---------------------------------------------------------------- 频道信息 ChannelInfo

// UpsertChannelInfos 批量写入频道信息，按 MixNo 去重更新（同一 MixNo 以本次
// 接口返回为准整体覆盖），同时维护 M3u8Mapping 的自动分组
// （对应原 BeforeCreate/BeforeUpdate 钩子）。
func (s *Store) UpsertChannelInfos(list []model.ChannelInfo) {
	if len(list) == 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range list {
		ch := list[i]
		ch.ProcessData()
		groups := ch.AutoGroupsFor()
		if groups != "" {
			m := s.mappings[ch.CommName]
			m.CommName = ch.CommName
			m.AutoGroups = groups
			s.mappings[ch.CommName] = m
		}
		ch.UpdatedAt = now
		s.chanInfos[ch.MixNo] = ch
	}
	s.bumpVersion()
}

// ListChannelInfos 返回全部频道信息，按 MixNo 排序（等价于原来的 mix_no asc / 全量查询）。
func (s *Store) ListChannelInfos() []model.ChannelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.ChannelInfo, 0, len(s.chanInfos))
	for _, ch := range s.chanInfos {
		out = append(out, ch)
	}
	model.SortChannelInfos(out)
	return out
}

// AggregateChannelInfosByCommName 按 CommName 聚合频道信息：同名频道
// （CCTV-1HD / CCTV-1 等不同清晰度）合并成一条，取较大的 Code/ChID。
func (s *Store) AggregateChannelInfosByCommName() []model.ChannelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grouped := make(map[string]model.ChannelInfo)
	for _, ch := range s.chanInfos {
		key := ch.CommName
		g, ok := grouped[key]
		if !ok {
			grouped[key] = ch
			continue
		}
		if ch.Code > g.Code {
			g.Code = ch.Code
		}
		if ch.ChID > g.ChID {
			g.ChID = ch.ChID
		}
		grouped[key] = g
	}
	out := make([]model.ChannelInfo, 0, len(grouped))
	for _, g := range grouped {
		out = append(out, g)
	}
	return out
}

// ChannelInfoCount 返回频道信息条数。
func (s *Store) ChannelInfoCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.chanInfos)
}

// CleanChannelInfosBefore 删除 UpdatedAt 早于 t 的频道信息，返回删除条数。
// 每轮拉取都会刷新所有仍在线频道的 UpdatedAt，因此超期条目不可能是正常
// 频道——只可能是运营商标里已下架、接口不再返回的那些。
func (s *Store) CleanChannelInfosBefore(t time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, ch := range s.chanInfos {
		if ch.UpdatedAt.Before(t) {
			delete(s.chanInfos, k)
			n++
		}
	}
	if n > 0 {
		s.bumpVersion()
	}
	return n
}

// ---------------------------------------------------------------- 节目单 EPGDetails

// ReplaceEPG 用新数据整体替换指定频道的节目单（对应原来的先删后插 + 按 ID 去重更新）。
// 同批次内 ID 重复时保留后写入的一条（等价于 ON DUPLICATE KEY UPDATE）。
// 入库时将完整的 EPGDetails 转换为瘦身结构 EPGItem，ID 仅用于去重，不落存储。
func (s *Store) ReplaceEPG(commName string, epgs []model.EPGDetails) {
	dedup := make(map[string]model.EPGDetails, len(epgs))
	for _, e := range epgs {
		dedup[e.ID] = e
	}
	sorted := make([]model.EPGDetails, 0, len(dedup))
	for _, e := range dedup {
		sorted = append(sorted, e)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartTime != sorted[j].StartTime {
			return sorted[i].StartTime < sorted[j].StartTime
		}
		return strings.Compare(sorted[i].ID, sorted[j].ID) < 0
	})
	list := make([]model.EPGItem, 0, len(sorted))
	for _, e := range sorted {
		list = append(list, model.EPGItem{
			Name:      e.Name,
			StartTime: e.StartTime,
			EndTime:   e.EndTime,
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epg[commName] = list
	s.bumpVersion()
}

// QueryEPG 查询指定频道名列表中结束时间晚于 minEndTimeMs 的节目单，
// 返回按频道名分组的切片，组内按 StartTime 升序。
func (s *Store) QueryEPG(commNames []string, minEndTimeMs int64) map[string][]model.EPGItem {
	nameSet := make(map[string]struct{}, len(commNames))
	for _, n := range commNames {
		nameSet[n] = struct{}{}
	}
	out := make(map[string][]model.EPGItem, len(commNames))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, list := range s.epg {
		if _, ok := nameSet[name]; !ok {
			continue
		}
		for _, e := range list {
			if e.EndTime > minEndTimeMs {
				out[name] = append(out[name], e)
			}
		}
	}
	return out
}

// EPGCount 返回节目单总条数。
func (s *Store) EPGCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, list := range s.epg {
		n += len(list)
	}
	return n
}

// CleanEPGBefore 删除结束时间早于 endTimeMs 的节目单条目，返回删除条数。
func (s *Store) CleanEPGBefore(endTimeMs int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for name, list := range s.epg {
		kept := list[:0]
		for _, e := range list {
			if e.EndTime >= endTimeMs {
				kept = append(kept, e)
			} else {
				n++
			}
		}
		if len(kept) == 0 {
			delete(s.epg, name)
		} else {
			s.epg[name] = kept
		}
	}
	if n > 0 {
		s.bumpVersion()
	}
	return n
}

// ---------------------------------------------------------------- M3u8Mapping

// GetMapping 按 CommName 查询映射。
func (s *Store) GetMapping(commName string) (model.M3u8Mapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.mappings[commName]
	return m, ok
}

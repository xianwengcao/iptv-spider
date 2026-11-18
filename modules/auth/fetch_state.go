package auth

import (
	"fmt"
	"sync"
	"time"

	"iptv-spider-sh/global"
)

// dataKind 标识一类数据集，用于清理任务的前置校验与日志可读性。
type dataKind string

const (
	dataChannelInfo dataKind = "频道信息(ChannelInfo)"
	dataProg        dataKind = "节目单(EPG)"
)

// fetchState 记录各数据集最近一次成功拉取的时间，以及最近一次尝试是否失败。
//
// 为什么需要它：改用内存存储后，每类数据在进程里都只有"唯一一份"，
// 清理任务一旦在某轮拉取失败之后执行，就可能把服务正在使用的数据删光：
//   - 频道信息（ChannelInfo）每日拉取一次，连续失败两天，
//     CleanChannelInfoData 就会把频道信息清空，EPG 与 m3u 同时变空；
//   - 节目单（EPG）同理，拉取长期失败后按时间清理会把残留也一并删掉。
//
// 因此清理任务不是"到点就删"，而是"确认对应数据刚被成功刷新过才删"：
// 只有最新一轮拉取成功时，超期条目才必定是残留（已下线的频道、已过期的节目），
// 删除才是安全的；拉取失败时宁可留着旧数据继续服务。
//
// 频道播放详情（Channel）不走这套机制：它在每轮认证时用快照整体替换
// （store.ReplaceChannels），不存在"按时间猜"的模糊地带。
type fetchState struct {
	mu   sync.RWMutex
	last map[dataKind]time.Time // 最近一次成功拉取的时间
	fail map[dataKind]bool      // 最近一次尝试是否失败
	note map[dataKind]string    // 最近一次失败的原因（仅用于日志）
}

var fetchTracker = &fetchState{
	last: make(map[dataKind]time.Time, 3),
	fail: make(map[dataKind]bool, 3),
	note: make(map[dataKind]string, 3),
}

// markFetchOK 记录某数据集本轮拉取成功（新数据已写入存储）。
func markFetchOK(k dataKind) {
	fetchTracker.mu.Lock()
	defer fetchTracker.mu.Unlock()
	fetchTracker.last[k] = time.Now()
	fetchTracker.fail[k] = false
	delete(fetchTracker.note, k)
}

// markFetchFailed 记录某数据集本轮拉取失败（未写入任何新数据）。
func markFetchFailed(k dataKind, reason string) {
	fetchTracker.mu.Lock()
	defer fetchTracker.mu.Unlock()
	fetchTracker.fail[k] = true
	fetchTracker.note[k] = reason
}

// allowClean 判断某数据集此刻是否允许执行清理。
//
// maxAge 为该数据集的删除阈值：只有最近一次成功刷新的时间落在阈值之内，
// 超期条目才必定是"本轮没有被刷新"的残留；若最新数据本身已经超过阈值，
// 说明拉取早就失败了，此时清理会把唯一一份数据删光。
func allowClean(k dataKind, maxAge time.Duration) bool {
	fetchTracker.mu.RLock()
	last, ok := fetchTracker.last[k]
	failed := fetchTracker.fail[k]
	note := fetchTracker.note[k]
	fetchTracker.mu.RUnlock()

	if !ok {
		global.LOG.Warn(fmt.Sprintf("跳过清理 %s: 本次运行尚未成功拉取过该数据, 保留现有数据", k))
		return false
	}
	if failed {
		global.LOG.Warn(fmt.Sprintf("跳过清理 %s: 最近一轮拉取失败(%s), 保留现有数据", k, note))
		return false
	}
	if age := time.Since(last); age > maxAge {
		global.LOG.Warn(fmt.Sprintf(
			"跳过清理 %s: 数据已 %.1f 小时未成功刷新, 超过清理阈值 %.1f 小时, 继续清理可能删掉唯一一份数据",
			k, age.Hours(), maxAge.Hours()))
		return false
	}
	return true
}

package auth

import (
	"fmt"
	"time"

	"github.com/golang-module/carbon"
	"iptv-spider-sh/global"
	"iptv-spider-sh/store"
)

// 各数据集的保留时长。清理阈值同时作为"数据是否新鲜"的判断依据：
// 只有最新数据落在阈值之内，超期条目才必定是残留（见 allowClean）。
//
// 频道播放详情（Channel）不在此列：它是认证页面的一份快照，
// 已下线的频道在每轮认证时由 store.ReplaceChannels 精确剔除，
// 既不需要、也无法按时间戳判断。
const (
	epgKeepDuration         = 8 * 24 * time.Hour // 节目单保留 8 天
	channelInfoKeepDuration = 48 * time.Hour     // 频道信息保留 48 小时
)

// CleanEPGDetailsData 节目单数据清理
func CleanEPGDetailsData() {
	// 整组替换机制下过期节目基本已被覆盖，这里是兜底：
	// 若最近一轮拉取失败，跳过清理，避免把仅剩的旧节目删光。
	if !allowClean(dataProg, epgKeepDuration) {
		return
	}
	// 如果节目单的结束时间早于 8 天前，则删除该数据
	t := carbon.Now().SubDays(8).TimestampMilli()
	n := store.Default.CleanEPGBefore(t)
	global.LOG.Info(fmt.Sprintf("清理 EPGDetails;\t条件 end_time < %d;\t清理条目: %d", t, n))
}

// CleanChannelInfoData 频道信息数据清理
func CleanChannelInfoData() {
	// 频道信息每日拉取一次，连续失败两天时若照常清理，会把频道信息清空，
	// EPG 与 m3u 同时变空。
	if !allowClean(dataChannelInfo, channelInfoKeepDuration) {
		return
	}
	// 如果频道信息的更新时间大于48小时，则删除该数据
	t := carbon.Now().SubHours(48)
	n := store.Default.CleanChannelInfosBefore(t.ToStdTime())
	global.LOG.Info(fmt.Sprintf("清理 ChannelInfo;\t条件 updated_at < %s;\t清理条目: %d", t.ToString(), n))
}

package model

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"iptv-spider-sh/config"
)

// ChannelInfo 频道信息，来源于 getChannelList 接口。
//
// 只保留会被后续使用的字段：Code/ChID 拼节目单请求参数，Name→CommName 供
// 聚合，IsHD/Is4K 用于同频道多清晰度去重，UpdatedAt 供清理任务判断。
// 接口里其余字段（tsTime / authCode / mediaID / isTs / isCharge）全项目从不
// 读取，已删除——JSON 里多出来的键会被忽略。
//
// 曾有的 IsShow / IsPullEPG 两个开关也已删除：它们只在内存中被设为 true，
// 没有任何代码能把它们置为 false。"隐藏某个频道"由配置项
// epg.exclude_channels 在生成 m3u 时按 CommName 过滤实现。
type ChannelInfo struct {
	UpdatedAt time.Time
	Code      string `json:"code"`
	Name      string `json:"name"`
	ChID      string `json:"ID"`
	MixNo     string `json:"mixNo"`
	IsHD      bool   `json:"-"`
	Is4K      bool   `json:"-"`
	CommName  string `json:"-"`
}

func (h *ChannelInfo) processData() {
	name := strings.ToUpper(h.Name)
	h.CommName = name
	if strings.HasSuffix(name, "HD") {
		h.IsHD = true
		c := strings.ReplaceAll(name, "HD", "")
		h.CommName = strings.TrimSpace(c)
	} else if strings.HasSuffix(name, "4K") {
		h.IsHD = true
		h.Is4K = true
		if !strings.HasSuffix(name, "-4K") {
			c := strings.ReplaceAll(name, "4K", "")
			h.CommName = strings.TrimSpace(c)
		}
	} else if strings.Contains(name, "高清") {
		h.IsHD = true
		h.CommName = strings.ReplaceAll(name, "(高清)", "")
	}
}

// DefaultGroupRules 内置默认分组规则：config 未配置 epg.group_rules 时使用，
// 行为与旧版硬编码逻辑一致。
var DefaultGroupRules = []config.GroupRule{
	{Group: "央视", Match: []string{"CCTV", "CGTN"}},
	{Group: "卫视", Match: []string{"卫视"}},
	{Group: "购物", Match: []string{"购物"}},
	{Group: "空中课堂", Match: []string{"年级"}},
	{Group: "百事通", Match: []string{"百事通"}},
	{Group: "电影", Match: []string{"影"}},
}

// MatchGroup 按规则给频道分组：自上而下第一条命中的规则生效（关键词
// 大小写不敏感的包含匹配），都不命中归入"其他"。
func MatchGroup(name string, rules []config.GroupRule) string {
	upper := strings.ToUpper(name)
	for _, r := range rules {
		for _, kw := range r.Match {
			if kw != "" && strings.Contains(upper, strings.ToUpper(kw)) {
				return r.Group
			}
		}
	}
	return "其他"
}

// AutoGroupsFor 计算该频道对应的自动分组字符串（rules 为空时用内置默认规则）
func (h *ChannelInfo) AutoGroupsFor(rules []config.GroupRule) string {
	if len(rules) == 0 {
		rules = DefaultGroupRules
	}
	return MatchGroup(h.Name, rules)
}

// ProcessData 处理频道名派生字段（CommName/IsHD/Is4K），供 store 在入库前调用
func (h *ChannelInfo) ProcessData() {
	h.processData()
}

// RemoveDuplicateChannelInfo  ChannelInfo 数组去重
func RemoveDuplicateChannelInfo(in []ChannelInfo) []ChannelInfo {
	newMap := make(map[string]ChannelInfo, len(in))
	for _, child := range in {
		if ch, ok := newMap[child.CommName]; ok {
			newMap[child.CommName] = check(ch, child)
			continue
		}
		newMap[child.CommName] = child
	}
	var newArr []ChannelInfo
	for _, v := range newMap {
		newArr = append(newArr, v)
	}
	SortChannelInfos(newArr)
	return newArr
}

// SortChannelInfos 按 MixNo 排序（优先数字比较，其次字符串比较）
func SortChannelInfos(arr []ChannelInfo) {
	sort.Slice(arr, func(i, j int) bool {
		return CompareMixNo(arr[i].MixNo, arr[j].MixNo)
	})
}

// CompareMixNo 比较 MixNo：都是数字时按数值比较，否则按字符串比较。
// 返回 true 表示 a 应排在 b 前面。
func CompareMixNo(a, b string) bool {
	numi, errI := strconv.Atoi(a)
	numj, errJ := strconv.Atoi(b)
	if errI == nil && errJ == nil {
		return numi < numj
	}
	return strings.Compare(a, b) < 0
}

func check(c1, c2 ChannelInfo) ChannelInfo {
	if c1.Is4K {
		return c1
	} else if c2.Is4K {
		return c2
	} else if c1.IsHD {
		return c1
	}
	return c2
}

package auth

import (
	"bytes"
	"errors"
	"fmt"
	"iptv-spider-sh/config"
	"iptv-spider-sh/global"
	"iptv-spider-sh/model"
	"iptv-spider-sh/modules/m3u"
	"iptv-spider-sh/store"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-module/carbon"
)

const timeFormat = carbon.ShortDateTimeLayout + " -0700"

// compareStrings 比较两个字符串，支持中文排序
// 返回 true 表示 str1 应该排在 str2 前面
func compareStrings(str1, str2 string) bool {
	if str1 == str2 {
		return false
	}

	// 尝试数字比较（如果都是数字）
	num1, err1 := strconv.Atoi(str1)
	num2, err2 := strconv.Atoi(str2)
	if err1 == nil && err2 == nil {
		return num1 < num2
	}

	// 使用 strings.Compare 进行字符串比较
	// strings.Compare 返回：
	// -1 如果 str1 < str2
	// 0  如果 str1 == str2
	// 1  如果 str1 > str2
	return strings.Compare(str1, str2) < 0
}

// getChannelInfoList 获取全部频道信息列表（内存存储，已按 MixNo 排序）
func getChannelInfoList() []model.ChannelInfo {
	return store.Default.ListChannelInfos()
}

// getM3u8Mapping 获取频道映射信息
func getM3u8Mapping(commName string) (model.M3u8Mapping, bool) {
	if commName == "" {
		return model.M3u8Mapping{}, false
	}
	return store.Default.GetMapping(commName)
}

// GenerateM3u8 生成 m3u 播放列表。
// 返回 error 而不是 nil 字节：调用方必须能区分"生成失败"和"空列表"，
// 否则 API 会给下游返回 200 + 空文件（播放器会以为频道全没了）。
func GenerateM3u8(udpxy, xteve string) ([]byte, error) {
	// 配置空值检查
	if global.CONFIG == nil || global.CONFIG.Epg.XmlUrl == "" {
		global.LOG.Error("配置文件未正确加载")
		return nil, errors.New("配置文件未正确加载 (epg.xml_url 为空)")
	}

	m3uWriter := m3u.NewWriter()
	m3uWriter.WriteHeaderWithInfo(global.CONFIG.Epg.XmlUrl)

	// 从内存存储读取频道信息
	channelInfoList := getChannelInfoList()
	// 去重
	newChanInfo := model.RemoveDuplicateChannelInfo(channelInfoList)
	global.LOG.Debug(fmt.Sprintf("GenerateM3u8: 去重后频道数量 %d", len(newChanInfo)))

	// 构建映射表（与原版语义一致，两套索引）：
	//   按 igmp —— 所有非 id_lookup 条目（含配了 display_name 的）都参与：
	//     运营商列表里地址相同的频道会被覆盖 tvg-name/logo/分组，
	//     但保留自己的显示名（如 CCTV-5HD）；display_name 条目本身
	//     在下面的「新增」分支另出一条独立频道，两者并存。
	//   按 id  —— 仅 id_lookup: true 的条目：直播地址要用配置的 igmp，
	//     不能拿 igmp 按地址匹配（会误改占用该地址的其它频道）。
	mappingByURL := make(map[string]*config.ChannelMapping)
	mappingByID := make(map[string]*config.ChannelMapping)
	for i := range global.CONFIG.Epg.ChannelMappings {
		m := &global.CONFIG.Epg.ChannelMappings[i]
		if m.IdLookup {
			if m.Id != "" {
				mappingByID[m.Id] = m
			}
			continue
		}
		if m.Igmp != "" {
			mappingByURL[m.Igmp] = m
		}
	}

	// 构建最终列表：先加 channel_infos，再加未匹配的 ChannelMappings
	type M3uItem struct {
		Info    model.ChannelInfo
		Channel model.Channel
		Mapping *config.ChannelMapping
	}

	// 性能优化：从内存存储按 MixNo 一次性取出需要的频道详情
	channelMap := make(map[string]model.Channel, len(newChanInfo))
	for _, info := range newChanInfo {
		if channel, ok := store.Default.GetChannelByUserChannelID(info.MixNo); ok {
			channelMap[info.MixNo] = channel
		}
	}

	var finalList []M3uItem
	// processed / processedIDs 记录已输出的频道 URL 和 MixNo，
	// 用于后面跳过 ChannelMappings 中与已抓取频道重复的条目，避免 m3u 中出现重复频道
	processed := make(map[string]bool)
	processedIDs := make(map[string]bool)
	for _, info := range newChanInfo {
		channel, ok := channelMap[info.MixNo]
		if !ok {
			global.LOG.Warn(fmt.Sprintf("未找到频道详情 (MixNo: %s)，跳过该频道", info.MixNo))
			continue
		}

		var mapping *config.ChannelMapping
		if m, ok := mappingByURL[channel.ChannelURL]; ok {
			mapping = m
		} else if m, ok := mappingByID[info.MixNo]; ok {
			// id_lookup 命中：整条频道记录（TimeShiftURL、FCC）都来自该 id 的频道，
			// 只把直播地址换成配置的 igmp。FCC 不动——旧代码即此行为，
			// 且运营商列表里没有占用配置组播组的频道可供按地址查询。
			mapping = m
			if m.Igmp != "" && m.Igmp != channel.ChannelURL {
				channel.ChannelURL = m.Igmp
			}
		}

		processed[channel.ChannelURL] = true
		processedIDs[info.MixNo] = true

		finalList = append(finalList, M3uItem{
			Info:    info,
			Channel: channel,
			Mapping: mapping,
		})
	}

	// 加入 ChannelMappings 中未匹配的 IGMP 频道（跳过已在上面输出过的，防止重复）
	if global.CONFIG.Epg.ChannelMappings != nil {
		for _, m := range global.CONFIG.Epg.ChannelMappings {
			// 配了 display_name 的条目是独立频道，不与列表里的频道去重：
			// 即使 id / igmp 撞上，也要和运营商那条并存。
			if m.DisplayName == "" {
				if m.IdLookup && processedIDs[m.Id] {
					continue
				}
				if !m.IdLookup && processed[m.Igmp] {
					continue
				}
			}
			channel := model.Channel{}
			var ok bool
			if m.IdLookup {
				// 通过 id（user_channel_id）查找，仅借用 TimeShiftURL
				channel, ok = store.Default.GetChannelByUserChannelID(m.Id)
			} else {
				// 原有逻辑：通过 igmp（channel_url）查找
				channel, ok = store.Default.GetChannelByURL(m.Igmp)
			}
			if !ok {
				global.LOG.Info(fmt.Sprintf("未在存储中找到IGMP频道 (IGMP: %s)，使用空频道详情", m.Igmp))
			}

			// 强制保留配置中的 igmp 作为播放地址，不被存储的 ChannelURL 覆盖。
			// FCC 不动：非 id_lookup 时 channel 本来就是按 igmp 查出的，FCC 天然正确；
			// id_lookup 时整条记录借自该 id 的频道，FCC 随记录一起借来（与 catchup-source 同源）。
			channel.ChannelURL = m.Igmp

			// Name 用于排序和 M3U 逗号后显示名，优先使用 display_name
			displayName := m.Name
			if m.DisplayName != "" {
				displayName = m.DisplayName
			}
			info := model.ChannelInfo{
				MixNo:    m.Id,
				CommName: m.Name,
				Name:     displayName,
			}
			finalList = append(finalList, M3uItem{
				Info:    info,
				Channel: channel,
				Mapping: &m,
			})
		}
	}

	// 排序严格按 name_sequence，匹配键是「最终显示名」：
	//   #EXTINF ... group-title="...",<这里的名字>  —— 即 model.ChannelInfo.Name
	// 不是 tvg-name（CommName）。配了 display_name 的频道，Name 在构造时就已经是
	// display_name，所以它们靠 name_sequence 里写 display_name 来定位。
	orderMap := make(map[string]int)
	if global.CONFIG.Epg.NameSequence != nil {
		for i, n := range global.CONFIG.Epg.NameSequence {
			orderMap[n.Name] = i
		}
	}

	// 对 finalList 排序
	sort.SliceStable(finalList, func(i, j int) bool {
		nameI := finalList[i].Info.Name
		nameJ := finalList[j].Info.Name

		indexI, okI := orderMap[nameI]
		indexJ, okJ := orderMap[nameJ]

		// 情况1：两个频道都在排序表中
		if okI && okJ {
			// 按排序表顺序排序
			return indexI < indexJ
		}

		// 情况2：两个频道都不在排序表中
		if !okI && !okJ {
			// 使用辅助函数进行排序比较
			return compareStrings(finalList[i].Info.MixNo, finalList[j].Info.MixNo)
		}

		// 情况3：一个在排序表，一个不在 —— 在排序表中的优先
		return okI
	})
	// 构建排除集合
	excludeSet := make(map[string]struct{})
	for _, name := range global.CONFIG.Epg.ExcludeChannels {
		excludeSet[name] = struct{}{}
	}

	// ✅ 统一循环写入 m3u
	for _, item := range finalList {
		// 过滤 exclude_channels 中的频道
		if _, excluded := excludeSet[item.Info.CommName]; excluded {
			continue
		}
		info := item.Info
		channel := item.Channel
		// 获取频道映射信息
		m3u8Mapping, _ := getM3u8Mapping(info.CommName)
		if m3u8Mapping.AutoGroups == "" {
			m3u8Mapping.AutoGroups = autoGroupByName(info.Name)
		}

		// 默认 logo
		if m3u8Mapping.Logo == "" {
			logoBaseUrl := global.CONFIG.Epg.LogoUrl
			logoImageName := fmt.Sprintf("%s.png", info.CommName)
			m3u8Mapping.Logo = fmt.Sprintf("%s%s", logoBaseUrl, logoImageName)
		}

		// 用户自定义映射覆盖（名称用于 tvg-name；显示名已在排序前统一应用）
		if item.Mapping != nil {
			if item.Mapping.Name != "" {
				info.CommName = item.Mapping.Name
			}
			if item.Mapping.Logo != "" {
				m3u8Mapping.Logo = global.CONFIG.Epg.LogoUrl + item.Mapping.Logo
			}
			if item.Mapping.Group != "" {
				m3u8Mapping.CustomGroups = item.Mapping.Group
			}
		}

		uri := assemblyUrl(udpxy, xteve, channel.ChannelURL, channel.ChannelFCCIP, channel.ChannelFCCPort)
		if uri == "" {
			// 播放地址生成失败时跳过该频道：写入一行空 URL 会让频道出现在
			// 列表里但无法播放，比不出现更难排查。
			global.LOG.Warn(fmt.Sprintf("频道 %s 播放地址生成失败, 已跳过 (ChannelURL: %q)",
				info.CommName, channel.ChannelURL))
			continue
		}

		catchupSource := ""
		if channel.TimeShiftURL != "" {
			trimmed := strings.TrimPrefix(channel.TimeShiftURL, "rtsp://")
			catchupSource = fmt.Sprintf("%s%s%s", global.CONFIG.Epg.RtspUrl, trimmed, global.CONFIG.Epg.Playseek)
		}

		m3uWriter.WriteWithCatchup(uri, catchupSource, info, m3u8Mapping)
	}

	return m3uWriter.Bytes(), nil
}

// assemblyUrl 按播放模式拼装播放地址：xteve 优先，其次 udpxy，否则走 rtp_url（含 FCC）。
func assemblyUrl(udpxy, xteve, uri, fccIp, fccPort string) string {
	// 配置空值检查
	if global.CONFIG == nil || global.CONFIG.Epg.RtpUrl == "" {
		global.LOG.Error("配置文件未正确加载，无法生成URL")
		return ""
	}

	// 添加URL解析错误处理
	if uri == "" {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil {
		global.LOG.Error(fmt.Sprintf("URL解析失败 (URI: %s): %s", uri, err.Error()))
		return ""
	}
	// xteve模式
	if xteve == "true" {
		return fmt.Sprintf("udp://@%s", u.Host)
	}

	// udpxy模式
	if udpxy != "" {
		return fmt.Sprintf("http://%s/udp/%s", udpxy, u.Host)
	}

	// HTTP RTP + FCC 使用动态加载的 rtp_url
	if fccIp != "" && fccPort != "" {
		return fmt.Sprintf(
			"%s%s?fcc=%s:%s",
			global.CONFIG.Epg.RtpUrl, // 使用动态加载的 rtp_url
			u.Host,
			fccIp,
			fccPort,
		)
	}

	// HTTP RTP 无FCC 使用动态加载的 rtp_url
	return fmt.Sprintf(
		"%s%s",
		global.CONFIG.Epg.RtpUrl, // 使用动态加载的 rtp_url
		u.Host,
	)
}

func GenerateXmlTv(daysAgo int) ([]byte, error) {
	// 配置空值检查
	if global.CONFIG == nil || global.CONFIG.Epg.Generator == "" {
		global.LOG.Error("配置文件未正确加载")
		return nil, errors.New("配置文件未正确加载")
	}

	if daysAgo < 1 {
		daysAgo = 1
	} else if daysAgo > 7 {
		daysAgo = 7
	}
	var now = carbon.Now()
	// 取数据
	channelInfoList := getChannelInfoList()
	// 去重
	newChanInfo := model.RemoveDuplicateChannelInfo(channelInfoList)

	// 收集所有需要拉取EPG的频道名称
	var epgChannelNames []string
	for _, info := range newChanInfo {
		if info.CommName != "" {
			epgChannelNames = append(epgChannelNames, info.CommName)
		}
	}

	// 从内存存储批量查询所有EPG数据
	epgDataMap := store.Default.QueryEPG(epgChannelNames, now.SubDays(daysAgo).TimestampMilli())

	// 预估输出字节数，减少 bytes.Buffer 的多次扩容
	programCount := 0
	for _, list := range epgDataMap {
		programCount += len(list)
	}

	// 流式写出：不再构建整棵文档树（数万个 programme/title 节点对象），
	// 输出与原先的 xml.MarshalIndent 逐字节一致（见 model 包的 golden 测试）。
	var buf bytes.Buffer
	buf.Grow(programCount*180 + len(newChanInfo)*160 + 1024)
	sw := model.NewXmlTvStreamWriter(&buf)
	if err := sw.Header(global.CONFIG.Epg.Generator+" "+now.ToDateTimeString(), global.CONFIG.Epg.Source); err != nil {
		global.LOG.Error("节目表单生成出错: " + err.Error())
		return nil, errors.New("节目表单生成出错")
	}

	for _, info := range newChanInfo {
		chId := info.MixNo
		if err := sw.Channel(chId, info.CommName); err != nil {
			global.LOG.Error("节目表单生成出错: " + err.Error())
			return nil, errors.New("节目表单生成出错")
		}

		// 从映射表中获取EPG数据
		for _, epg := range epgDataMap[info.CommName] {
			// 直接格式化时间戳：比 carbon.CreateFromTimestampMilli().Layout() 快一个数量级，
			// 输出格式与 carbon.ShortDateTimeLayout + " -0700" 完全一致（本地时区）
			startTime := time.UnixMilli(epg.StartTime).Format(timeFormat)
			endTime := time.UnixMilli(epg.EndTime).Format(timeFormat)
			if err := sw.Programme(chId, startTime, endTime, epg.Name); err != nil {
				global.LOG.Error("节目表单生成出错: " + err.Error())
				return nil, errors.New("节目表单生成出错")
			}
		}
	}

	if err := sw.Footer(); err != nil {
		global.LOG.Error("节目表单生成出错: " + err.Error())
		return nil, errors.New("节目表单生成出错")
	}
	return buf.Bytes(), nil
}

func autoGroupByName(name string) string {
	if strings.Contains(name, "CCTV") {
		return "央视"
	} else if strings.Contains(name, "卫视") {
		return "卫视"
	} else if strings.Contains(name, "购物") {
		return "购物"
	} else if strings.Contains(name, "年级") {
		return "空中课堂"
	} else if strings.Contains(name, "百事通") {
		return "百事通"
	} else if strings.Contains(name, "影") {
		return "电影"
	}
	return "其他"
}

package api

import (
	"errors"
	"iptv-spider-sh/global"
	"iptv-spider-sh/modules/auth"
	"iptv-spider-sh/store"
	"iptv-spider-sh/utils"
	"strconv"
	"sync"
	"time"

	"github.com/kataras/iris/v12"
)

// prevKeys 记录每个端点（接口名 + 请求参数）上一次写入的完整缓存 key（含版本号）。
// 新版本条目写入后立即删除旧条目——否则旧版本可能滞留内存直到 cacheTTL 过期，
// 而 XMLTV 一份缓存可达数 MB，多个版本叠加会白白占用内存。
var (
	prevKeysMu sync.Mutex
	prevKeys   = make(map[string]string)
)

// cacheTTL API 响应缓存的兜底回收周期。
// 缓存 key 已拼接数据版本号，数据更新后旧缓存立即被删除（见 prevKeys），
// 此 TTL 只影响"再也不会被请求的孤儿条目"占用内存多久后被回收，
// 不影响数据新鲜度，因此直接内置，无需配置。
const cacheTTL = 240 * time.Minute

// cachedGenerate 带版本号的缓存响应逻辑。
//
// 缓存 key 拼接了内存数据的版本号：数据每次更新（拉取频道/节目单、清理等）
// 版本号都会递增，旧版本缓存即刻作废，因此下游应用**任何时刻**来拉取，
// 拿到的都是当前数据的最新生成结果，无需依赖缓存过期时间。
// cacheTTL 仅作为旧缓存条目的兜底回收周期（正常情况下旧版本
// 会在新版本生成时被立即删除，见 prevKeys）。
//
// 生成失败时返回 error，由调用方转成 5xx：之前错误被 singleflight 吞掉，
// 调用方拿到 nil 字节但仍然返回 200 + 空 body + Content-Type: xml，
// 播放器/xteve 会认为节目单就是空的，可能把已有的节目表直接刷掉。
func cachedGenerate(ctx iris.Context, name string, params []string, gen func() ([]byte, error)) ([]byte, error) {
	// 端点级基础 key（不含版本号），用于定位上一次的缓存条目
	baseKey := utils.CalcMD5KeyForRequest(append([]string{name}, params...)...)

	// 读取请求时刻的数据版本号
	version := strconv.FormatUint(store.Default.Version(), 10)
	reqMD5Key := utils.CalcMD5KeyForRequest(append(append([]string{name}, params...), version)...)

	// 缓存机制
	if buf, ok := global.CACHE.Get(reqMD5Key).([]byte); ok && len(buf) > 0 {
		return buf, nil
	}
	// 并发时合并请求
	resp, err, _ := global.ConcurrencyControl.Do(reqMD5Key, func() (interface{}, error) {
		buf, err := gen()
		if err != nil {
			return nil, err
		}
		if len(buf) == 0 {
			return nil, errors.New("生成结果为空")
		}
		global.CACHE.Put(reqMD5Key, buf, cacheTTL)

		// 新版本条目已就绪，立即回收同一端点的旧版本条目
		prevKeysMu.Lock()
		if old, ok := prevKeys[baseKey]; ok && old != reqMD5Key {
			_ = global.CACHE.Delete(old)
		}
		prevKeys[baseKey] = reqMD5Key
		prevKeysMu.Unlock()

		return buf, nil
	})
	if err != nil {
		return nil, err
	}
	buf, ok := resp.([]byte)
	if !ok || len(buf) == 0 {
		return nil, errors.New("生成结果为空")
	}
	return buf, nil
}

func InitApiRouters(rg iris.Party) {
	rg.Get("/schedule", schedule)
	rg.Get("/run", func(ctx iris.Context) {
		// 不带参数等同 update-all；显式传 task=update-all 也可以。
		// 其余任务名已移除，传了直接返回 400，而不是假装 OK
		taskName := ctx.FormValueDefault("task", "update-all")
		if taskName != "update-all" {
			ctx.StatusCode(iris.StatusBadRequest)
			ctx.WriteString("unknown task: " + taskName)
			return
		}

		go func() {
			// singleflight 合并并发触发：上一轮还在跑时，重复触发不会叠加执行
			global.ConcurrencyControl.Do(taskName, func() (interface{}, error) {
				c := auth.GetGlobalClient()
				// 认证 + 频道列表 + 节目单，与 cron 的一轮完全一致
				c.RunRound()
				return nil, nil
			})
		}()
		ctx.WriteString("OK")
	})

	// 健康检查：返回内存数据的规模与版本号，用于探活和数据新鲜度确认
	rg.Get("/healthz", func(ctx iris.Context) {
		ctx.JSON(map[string]interface{}{
			"status":           "ok",
			"version":          store.Default.Version(),
			"channelCount":     store.Default.ChannelCount(),
			"channelInfoCount": store.Default.ChannelInfoCount(),
			"epgCount":         store.Default.EPGCount(),
		})
	})

	rg.Get("/m3u8", generateM3u8)

	rg.Get("/epg", generateXmlTv)

}

func schedule(ctx iris.Context) {
	type s struct {
		ID       int
		PreTime  time.Time
		NextTime time.Time
	}
	var schedule []s
	for _, entry := range global.CRON.Entries() {
		schedule = append(schedule, s{
			ID:       int(entry.ID),
			PreTime:  entry.Prev,
			NextTime: entry.Next,
		})
	}
	ctx.JSON(schedule)
}

// 生成m3u8文件 节目去重
func generateM3u8(ctx iris.Context) {
	buf, err := cachedGenerate(ctx, "generateM3u8", nil, func() ([]byte, error) {
		return auth.GenerateM3u8()
	})
	if err != nil {
		global.LOG.Error("生成 m3u 失败: " + err.Error())
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.WriteString("generate m3u failed: " + err.Error())
		return
	}
	ctx.Header("Content-Disposition", "attachment; filename=iptv.m3u")
	ctx.Binary(buf)
}

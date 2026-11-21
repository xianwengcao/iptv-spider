package api

import (
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/context"
	"iptv-spider-sh/global"
	"iptv-spider-sh/modules/auth"
)

// generateXmlTv 生成xmlTv文件 节目去重
// 缓存 key 拼接数据版本号（见 cachedGenerate）：节目单更新后旧缓存自动失效。
func generateXmlTv(ctx iris.Context) {
	raw := ctx.FormValueDefault("daysAgo", "3")

	d, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		// 之前是 200 + 错误文本，下游会把 "strconv.Atoi: ..." 当成 XML 解析
		global.LOG.Warn("generateXmlTv: daysAgo 参数非法: " + raw)
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.WriteString("invalid daysAgo: " + raw)
		return
	}

	// 归一化到固定档位 {1, 3, 7}：不在档位上的值向上取整到最近的档位。
	// 向上取整保证下游拿到的是请求天数的超集（少给可能让播放器缺历史节目）。
	// 收敛档位的目的：1~7 之间任意取值会让每个 daysAgo 各占一份数 MB 的
	// 缓存条目（7 份可叠到 ~30MB）；收敛后缓存最多 3 份（~12MB 封顶）。
	// d <= 0 的非法值也会落进第一档（1），与原先"钳到 1"的行为一致。
	d = normalizeDaysAgo(d)
	daysAgo := strconv.Itoa(d)

	buf, err := cachedGenerate(ctx, "generateXmlTv", []string{daysAgo}, func() ([]byte, error) {
		return auth.GenerateXmlTv(d)
	})
	if err != nil {
		global.LOG.Error("生成 XMLTV 失败: " + err.Error())
		ctx.StatusCode(iris.StatusInternalServerError)
		ctx.WriteString("generate xmltv failed: " + err.Error())
		return
	}
	ctx.ContentType(context.ContentXMLHeaderValue)
	ctx.Write(buf)
}

// epgDayTiers daysAgo 允许的固定档位（升序）。收敛档位以限制缓存份数：
// 每一档对应一份数 MB 的 XMLTV 缓存，档位越多内存上限越高。
var epgDayTiers = [3]int{1, 3, 7}

// normalizeDaysAgo 把任意请求值向上取整到最近的档位。
// 返回值一定落在 epgDayTiers 内，直接用作缓存 key 的一部分。
func normalizeDaysAgo(d int) int {
	for _, t := range epgDayTiers {
		if d <= t {
			return t
		}
	}
	return epgDayTiers[len(epgDayTiers)-1]
}

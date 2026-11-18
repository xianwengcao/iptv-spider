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
	raw := ctx.FormValueDefault("daysAgo", "1")

	d, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		// 之前是 200 + 错误文本，下游会把 "strconv.Atoi: ..." 当成 XML 解析
		global.LOG.Warn("generateXmlTv: daysAgo 参数非法: " + raw)
		ctx.StatusCode(iris.StatusBadRequest)
		ctx.WriteString("invalid daysAgo: " + raw)
		return
	}

	// 归一化到 [1, 7]（与 GenerateXmlTv 内部一致），并用归一化值作缓存 key：
	// 否则 "1"、"01"、" 1"、"999" 会各占一份几 MB 的缓存条目。
	if d < 1 {
		d = 1
	} else if d > 7 {
		d = 7
	}
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

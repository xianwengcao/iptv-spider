package initialize

import (
	"fmt"
	"strings"

	"iptv-spider-sh/global"
	"iptv-spider-sh/modules/netbind"
)

// NetBind 依据配置文件初始化「出站网卡绑定」。
//
// system.dev 是总开关：填写网卡名（eth0 / pppoe-wan / br-lan / eth0.85 ...）
// 或该网卡上的本机 IPv4 地址后，程序所有对外通信
// （IPTV 认证 / EPG / 频道列表）都会从该网卡发出。
// 留空表示不绑定（保持原有走系统路由表的行为）。
func NetBind() netbind.Group {
	sys := global.CONFIG.System
	bindDNS := sys.DevDNS

	def, err := netbind.New(netbind.Options{
		Dev:     sys.Dev,
		BindDNS: bindDNS,
		Logger:  global.LOG,
	})
	if err != nil {
		bindLogError("出站网卡绑定初始化失败，本次不启用：" + err.Error())
		return netbind.Group{
			Default: netbind.Disabled(),
		}
	}

	g := netbind.Group{
		Default: def,
	}

	// HTTP：兜底替换进程级默认传输，覆盖所有直接使用 http.DefaultClient /
	// http.DefaultTransport 的库。
	// resty 客户端会在 modules/http_client 里显式使用 Default 的 Transport。
	g.Default.ApplyHTTPDefault()

	if def.Enabled() {
		bindLogInfo("出站网卡绑定已启用：" + def.Describe())
		bindLogInfo(fmt.Sprintf("  └─ IPTV/HTTP : %s", g.Default.Describe()))
	} else if strings.TrimSpace(sys.Dev) != "" {
		bindLogInfo("system.dev 解析失败，出站网卡绑定未启用")
	} else {
		bindLogInfo("出站网卡绑定未启用（system.dev 为空，走系统默认路由）")
	}
	return g
}

func bindLogInfo(msg string) {
	if global.LOG != nil {
		global.LOG.Info(msg)
	} else {
		fmt.Println("[netbind] " + msg)
	}
}

func bindLogError(msg string) {
	if global.LOG != nil {
		global.LOG.Error(msg)
	} else {
		fmt.Println("[netbind] " + msg)
	}
}

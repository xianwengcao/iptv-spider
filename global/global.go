package global

import (
	"github.com/astaxie/beego/cache"
	"github.com/robfig/cron/v3"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"iptv-spider-sh/config"
	"iptv-spider-sh/modules/netbind"
)

var (
	CACHE              cache.Cache
	VIPER              *viper.Viper
	CONFIG             *config.Server
	LOG                *zap.Logger
	CRON               *cron.Cron
	ConcurrencyControl = &singleflight.Group{}

	// BIND 出站网卡绑定，由 initialize.NetBind() 依据 config 的 system.dev 初始化。
	// 零值表示未启用，所有方法都是安全的空实现。
	BIND netbind.Group
)

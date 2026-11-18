package initialize

import (
	"github.com/astaxie/beego/cache"
)

// Cache 初始化内存缓存（仅此一种模式）。
// 本程序是单实例单进程，缓存均为可随时重建的派生数据，
// 无多实例共享需求，故 Redis 等外部缓存支持已移除。
// interval 为过期条目的回收扫描间隔（秒）。
func Cache() cache.Cache {
	bm, err := cache.NewCache("memory", `{"interval": 60}`)
	if err != nil {
		panic(err)
	}
	if bm == nil {
		panic("initialize failed, Bm is nil!")
	}
	return bm
}

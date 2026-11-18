package initialize

import (
	"flag"
	"fmt"
	"github.com/spf13/viper"
	"iptv-spider-sh/global"
	"os"
)

func Viper(path ...string) *viper.Viper {
	var config string
	if len(path) == 0 {
		flag.StringVar(&config, "c", "", "choose config file.")
		flag.Parse()
		if config == "" { // 优先级: 命令行 > 环境变量 > 默认值
			if configEnv := os.Getenv("GO_CONFIG"); configEnv == "" {
				config = "config.yaml"
				fmt.Printf("您正在使用config的默认值,config的路径为%v\n", config)
			} else {
				config = configEnv
				fmt.Printf("您正在使用GO_CONFIG环境变量,config的路径为%v\n", config)
			}
		} else {
			fmt.Printf("您正在使用命令行的-c参数传递的值,config的路径为%v\n", config)
		}
	} else {
		config = path[0]
		fmt.Printf("您正在使用func Viper()传递的值,config的路径为%v\n", config)
	}

	v := viper.New()
	v.SetConfigFile(config)
	v.SetConfigType("yaml")
	err := v.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}
	// 注意：不启用 viper 的 WatchConfig 热更新。
	// 原因有二：
	//  1. OnConfigChange 里 Unmarshal(&global.CONFIG) 与 HTTP goroutine
	//     的并发读构成无锁数据竞争（未定义行为）。
	//  2. 改配置本就无法重新注册 cron 任务 / 网卡绑定，热更新"既不安全也不完整"。
	// 配置修改后重启程序生效。
	// 反序列化失败必须终止：之前只 fmt.Println 后继续运行，会带着半空的配置
	// 启动（例如 epg.rtp_url 丢失 → m3u 里所有播放地址为空、日志还好好的），
	// 排查成本远高于直接崩在启动阶段。
	if err := v.Unmarshal(&global.CONFIG); err != nil {
		panic(fmt.Errorf("Fatal error config unmarshal (%s): %w", config, err))
	}

	return v
}

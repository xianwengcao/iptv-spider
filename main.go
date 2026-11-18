package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-module/carbon"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/recover"
	"github.com/robfig/cron/v3"
	"iptv-spider-sh/global"
	"iptv-spider-sh/initialize"
	"iptv-spider-sh/modules/auth"
	"iptv-spider-sh/modules/netbind"
	"iptv-spider-sh/router"
)

// netCheck 只做出站网卡连通性自检就退出，用于排查「认证接口 i/o timeout」这类问题。
// 必须在 initialize.Viper() 之前注册：Viper 内部会调用 flag.Parse()。
var netCheck = flag.Bool("netcheck", false, "只做出站网卡连通性自检并退出")

func main() {
	initialize.Viper()
	if *netCheck {
		runNetCheck()
		return
	}
	global.LOG = initialize.Zap()
	// 出站网卡绑定：必须在 Cache() 之前初始化
	global.BIND = initialize.NetBind()
	global.CACHE = initialize.Cache()
	// 数据存储：store.Default（内存存储，替代原先的 MySQL 持久层），无需初始化

	initialize.InitCron()

	app := iris.New()

	app.Use(recover.New())

	router.InitRouters(app)

	stb := global.CONFIG.Stb
	client, err := auth.NewGlobalClient(stb.UID, stb.SN, stb.MAC, stb.IP)
	if err != nil {
		// 用 Fatal（退出码 1）而不是 Error + return（退出码 0）：
		// 配置写错时进程以 0 退出，procd / systemd 会认为"正常结束"而不再拉起，
		// 服务就这么静默地停在那儿。非 0 退出码才能触发守护进程重启并暴露问题。
		global.LOG.Fatal("New AuthClient: " + err.Error())
	}
	addSpiderCron(client)
	addDataCleanCron()

	// 启动一个协程跑第一轮：认证 → 频道列表 → 节目单
	go func() {
		client.RunRound()
	}()

	app.ConfigureHost(configHost)
	// 优雅关闭时 app.Run 返回 nil（WithoutServerError 忽略了 ErrServerClosed）；
	// 其余错误（端口被占用、监听地址非法）以前被 `_ =` 丢弃，进程以 0 退出，
	// 守护进程不会重启，表现为"启动一下就没了"。
	if err := app.Run(iris.Addr(global.CONFIG.System.Addr), iris.WithoutServerError(iris.ErrServerClosed)); err != nil {
		global.LOG.Fatal("HTTP 服务异常退出: " + err.Error())
	}

}

// 爬虫定时器
//
// 只有一个轮次任务：按 epg.fetch_cron 的节奏，把"认证 → 频道信息列表 →
// 节目单"整轮跑一遍。
//
// 为什么要每轮都认证：会话只在拉取的那十几秒里被用到，两轮之间相隔数小时，
// EPG 侧的 JSESSIONID 早已过期。心跳能续命，但代价是常年维护一个用不到的
// 会话，还得额外写一套"会话还活着吗"的探测逻辑（信任窗口、三态分类、补拉、
// 失败回滚）。改为每轮开头认证一次（不到 1 秒）之后，会话变成轮内临时资源，
// 上述分支全部消失，同时频道播放详情（组播地址 / FCC 节点）也随每轮刷新，
// 不再需要单独的"每周重新认证"。
func addSpiderCron(c *auth.Client) {
	var roundID cron.EntryID
	roundID = mustAddCron(global.CONFIG.Epg.FetchCron, "拉取轮次(认证+频道列表+节目单)", func() {
		c.RunRound()
		logNexTime("Task: 拉取轮次;\t\t\tNextTime: ", roundID)
	})
}

// 数据清理定时器
func addDataCleanCron() {
	var taskId cron.EntryID
	// 整组替换机制下过期数据基本已被覆盖，清理任务只是兜底安全网，每天一次足够
	taskId = mustAddCron("@daily", "清理过期数据", func() {
		auth.CleanEPGDetailsData()
		auth.CleanChannelInfoData()
		logNexTime("Task: 清理过期数据;\t\t\tNextTime: ", taskId)
	})
}

// mustAddCron 注册定时任务，表达式解析失败时直接终止进程。
//
// cron 使用 WithSeconds() 创建，spec 必须是 6 段（秒 分 时 日 月 周）。
// 之前 AddFunc 的错误被静默忽略，表达式写错（比如按 5 段习惯少写秒位）
// 时任务不会注册、程序照常启动、日志里毫无痕迹，节目单会永远不更新。
// fail-fast 让这类配置错误在启动阶段就暴露出来。
func mustAddCron(spec, name string, f func()) cron.EntryID {
	id, err := global.CRON.AddFunc(spec, f)
	if err != nil {
		global.LOG.Fatal(fmt.Sprintf("定时任务 %s 注册失败 (spec: %q): %s", name, spec, err.Error()))
	}
	logNexTime(fmt.Sprintf("Add Task: %s;\t\tNextTime: ", name), id)
	return id
}

func logNexTime(msg string, id cron.EntryID) {
	t := global.CRON.Entry(id).Next
	global.LOG.Info(msg + carbon.FromStdTime(t).ToString())
}

func configHost(su *iris.Supervisor) {
	su.RegisterOnShutdown(func() {
		// 优雅关闭：先等 cron 里正在执行的任务跑完（CRON.Stop 会阻塞到
		// 当前运行中的任务结束，例如一轮 ~20 秒的节目单拉取），避免进程
		// 退出时把拉取轮次截断在半途；最后冲刷日志缓冲。
		global.LOG.Info("正在停止定时任务（等待运行中的任务结束）...")
		stopCtx := global.CRON.Stop()
		<-stopCtx.Done()
		global.LOG.Info("定时任务已全部停止")
		_ = global.LOG.Sync()
		println("Server Closed")
	})
}

// runNetCheck 打印「哪张网卡能到达 IPTV 认证服务器」的自检报告。
//
// 用法：./iptv-spider-sh -netcheck
// 它会依次用 system.dev 指定的出口、系统默认路由、以及其余每张网卡去连认证服务器，
// 直接给出「该把 system.dev 设成哪张网卡」的结论，不需要真的跑一遍认证流程。
func runNetCheck() {
	stb := global.CONFIG.Stb
	// 认证流程与 netcheck 现在读取同一个配置项 stb.auth_host，
	// 探测结果与实际认证行为必然一致；配置为空时回退内置默认值。
	target := auth.NormalizeHostPort(stb.AuthHost, "7001", auth.Default4kLogAuthAddr)
	if stb.AuthHost == "" {
		fmt.Printf("配置里没有 stb.auth_host，使用内置默认值 %s\n\n", auth.Default4kLogAuthAddr)
	}

	netbind.Diagnose(context.Background(), global.CONFIG.System.Dev, []string{target}, 3*time.Second, os.Stdout)
}

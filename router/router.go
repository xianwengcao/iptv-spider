package router

import (
	"github.com/kataras/iris/v12"
	"iptv-spider-sh/router/api"
)

func InitRouters(app *iris.Application) {
	// 各个路由分组
	//app.UseRouter(middleware.Cors())
	// 各个路由分组
	apiRouterGroup := app.Party("/api")
	{
		api.InitApiRouters(apiRouterGroup)
	}

}

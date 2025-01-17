package gou

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yaoapp/kun/log"
	"github.com/yaoapp/kun/maps"

	"github.com/gin-gonic/gin"
	"github.com/yaoapp/gou/helper"
	"github.com/yaoapp/kun/exception"
	"github.com/yaoapp/xun"
)

// APIs 已加载API列表
var APIs = map[string]*API{}

// LoadAPIReturn 加载API
func LoadAPIReturn(source string, name string) (api *API, err error) {
	defer func() { err = exception.Catch(recover()) }()
	api = LoadAPI(source, name)
	return api, nil
}

// LoadAPI 加载API
func LoadAPI(source string, name string) *API {
	var input io.Reader = nil
	if strings.HasPrefix(source, "file://") {
		filename := strings.TrimPrefix(source, "file://")
		file, err := os.Open(filename)
		if err != nil {
			exception.Err(err, 400).Throw()
		}
		defer file.Close()
		input = file
	} else {
		input = strings.NewReader(source)
	}

	http := HTTP{}
	err := helper.UnmarshalFile(input, &http)
	if err != nil {
		exception.Err(err, 400).Ctx(maps.Map{"name": name}).Throw()
	}

	// Validate API
	uniquePathCheck := map[string]bool{}
	for _, path := range http.Paths {
		unique := fmt.Sprintf("%s.%s", path.Method, path.Path)
		if _, has := uniquePathCheck[unique]; has {
			exception.New("%s %s %s is already registered", 400, name, path.Method, path.Path).Throw()
		}
		uniquePathCheck[unique] = true
	}

	APIs[name] = &API{
		Name:   name,
		Source: source,
		HTTP:   http,
		Type:   "http",
	}
	return APIs[name]
}

// SelectAPI 读取已加载API
func SelectAPI(name string) *API {
	api, has := APIs[name]
	if !has {
		exception.New(
			fmt.Sprintf("API:%s; 尚未加载", name),
			500,
		).Throw()
	}
	return api
}

// ServeHTTP  启动HTTP服务
func ServeHTTP(server Server, shutdown *chan bool, onShutdown func(Server), middlewares ...gin.HandlerFunc) {
	router := gin.Default()
	ServeHTTPCustomRouter(router, server, shutdown, onShutdown, middlewares...)
}

// ServeHTTPCustomRouter 启动HTTP服务, 自定义路由器
func ServeHTTPCustomRouter(router *gin.Engine, server Server, shutdown *chan bool, onShutdown func(Server), middlewares ...gin.HandlerFunc) {

	// 设置路由
	SetHTTPRoutes(router, server, middlewares...)

	// 服务配置
	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("listen: %s", err)
		}
	}()

	// 接收关闭信号
	go func() {
		<-*shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Fatal("服务关闭失败: %s", err)
		}
		KillPlugins()
		onShutdown(server)
	}()

	// 服务终止时 关闭插件进程
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	<-quit
	KillPlugins()
}

// SetHTTPRoutes 设定路由
func SetHTTPRoutes(router *gin.Engine, server Server, middlewares ...gin.HandlerFunc) {
	// 添加中间件
	for _, handler := range middlewares {
		router.Use(handler)
	}

	// 错误处理
	router.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {

		var code = http.StatusInternalServerError

		if err, ok := recovered.(string); ok {
			c.JSON(code, xun.R{
				"code":    code,
				"message": fmt.Sprintf("%s", err),
			})
		} else if err, ok := recovered.(exception.Exception); ok {
			code = err.Code
			c.JSON(code, xun.R{
				"code":    code,
				"message": err.Message,
			})
		} else if err, ok := recovered.(*exception.Exception); ok {
			code = err.Code
			c.JSON(code, xun.R{
				"code":    code,
				"message": err.Message,
			})
		} else {
			c.JSON(code, xun.R{
				"code":    code,
				"message": fmt.Sprintf("%v", recovered),
			})
		}

		c.AbortWithStatus(code)
	}))

	// 加载API
	for _, api := range APIs {
		api.HTTP.Routes(router, server.Root, server.Allows...)
	}
}

// SetHTTPGuards 加载中间件
func SetHTTPGuards(guards map[string]gin.HandlerFunc) {
	HTTPGuards = guards
}

// AddHTTPGuard 添加中间件
func AddHTTPGuard(name string, guard gin.HandlerFunc) {
	HTTPGuards[name] = guard
}

// Reload 重新载入API
func (api *API) Reload() *API {
	api = LoadAPI(api.Source, api.Name)
	return api
}

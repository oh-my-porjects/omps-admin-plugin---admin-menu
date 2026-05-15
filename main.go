package main

// admin-meta：项目级管理后台菜单 / spec 的存储与对外接口
//
// 平台内置插件（每个项目的 runtime 自动加载），跟业务模块同款 plugin 机制：
//   - 数据存自己的 PG 表 admin_menu_specs（runtime 注入的 DB）
//   - admin-server AI 生成完 spec 之后，HTTP 推过来这里持久化
//   - runtime 主程序对外吐 /admin/_meta/menu 时内部代理本插件的 GET /menu
//   - 升级菜单逻辑：换 .so 文件即可，runtime 主程序不重启，业务模块继续跑
//
// 解耦点：runtime 启动后不再依赖 admin-server 拉 spec。admin-server 挂了
// 项目后台菜单照常显示，只是不能新增 / 重生 spec。
//
// 路由协议：
//   POST   /{admin_prefix}/api/admin-meta/specs/{module}  写入 spec（admin-server 推）
//   DELETE /{admin_prefix}/api/admin-meta/specs/{module}  删除某模块 spec
//   GET    /{admin_prefix}/api/admin-meta/specs/{module}  拿单模块完整 spec
//   GET    /{admin_prefix}/api/admin-meta/specs           列出所有模块 spec 元数据
//   GET    /{admin_prefix}/api/admin-meta/menu            聚合菜单（按模块分组）
//
// {admin_prefix} 部分用 Go 1.22 pattern 占位，runtime 鉴权中间件按
// X-API-Key 校验；本插件 handler 内不再重复鉴权。

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"
)

// PluginContext 必须跟 runtime/internal/plugin/interface.go 字段名/类型完全一致
// runtime 通过反射按字段名注入，缺字段静默跳过，多字段 build 不通过
type PluginContext struct {
	DB             *sql.DB
	Config         map[string]string
	Logger         *slog.Logger
	LifecycleCtx   context.Context
	RegisterWorker func() func()
	IsUnloading    func() bool

	Push      func(ctx context.Context, userID, code string, data any) (id int64, err error)
	Emit      func(userID, code string, data any) bool
	Broadcast func(ctx context.Context, userIDs []string, code string, data any) (ids []int64, err error)
	IsOnline  func(userID string) bool
	Audit     func(action string, before any, after any, extra map[string]any)

	RegisterAuth func(
		verify func(ctx context.Context, accessToken string) (userID string, expiresAt time.Time, refreshToken string, err error),
		refresh func(ctx context.Context, refreshToken string) (newAccess, newRefresh string, newExpiresAt time.Time, err error),
		checkSession func(ctx context.Context, userID, accessToken string) (valid bool, reason string),
	)
}

// Plugin runtime 通过 plugin.Lookup("Plugin") 加载本符号
var Plugin = &AdminMenuPlugin{}

// Routes runtime 通过 plugin.Lookup("Routes") 拿到全部路由声明
//
// path 用 /api/admin-meta/...（跟平台 plugin_contract 新规范一致：去 admin_prefix）。
// 鉴权由 runtime 主程序在 ServeHTTP 入口处理：
//   - 外部走 X-API-Key（环境的 admin_api_key）
//   - admin-server 这种内部调用走 X-Internal-Token 旁路（注入合法 admin session）
// task/inner_plugin.md §9 重命名为 admin-menu。
// 这里采用「双路径并存」策略：保留老 /api/admin-meta/* 路径兼容现有项目，
// 同时挂 /api/admin-menu/* 新路径让 admin-server 推送可以无缝切换。
// 完整重命名（删 admin-meta）需要等所有引用项目升级完毕（§9.4 + §15.1）。
var Routes = map[string]http.HandlerFunc{
	"POST /api/admin-meta/specs/{module}":   handleSpecWrite,
	"DELETE /api/admin-meta/specs/{module}": handleSpecDelete,
	"GET /api/admin-meta/specs/{module}":    handleSpecGet,
	"GET /api/admin-meta/specs":             handleSpecList,
	"GET /api/admin-meta/menu":              handleMenu,

	"POST /api/admin-menu/specs/{module}":   handleSpecWrite,
	"DELETE /api/admin-menu/specs/{module}": handleSpecDelete,
	"GET /api/admin-menu/specs/{module}":    handleSpecGet,
	"GET /api/admin-menu/specs":             handleSpecList,
	"GET /api/admin-menu/menu":              handleMenu,
}

// AdminMenuPlugin 实现 GamePlugin 接口
type AdminMenuPlugin struct {
	db     *sql.DB
	logger *slog.Logger
}

var version = "1.0.0"

func (p *AdminMenuPlugin) Name() string    { return "admin-menu" }
func (p *AdminMenuPlugin) Version() string { return version }

func (p *AdminMenuPlugin) Init(ctx PluginContext) error {
	p.db = ctx.DB
	p.logger = ctx.Logger
	if p.logger == nil {
		p.logger = slog.Default()
	}
	if err := ensureSchema(p.db); err != nil {
		return err
	}
	p.logger.Info("admin-meta plugin 初始化完成", "name", p.Name(), "version", p.Version())
	return nil
}

func (p *AdminMenuPlugin) Shutdown(ctx context.Context) error {
	p.logger.Info("admin-meta plugin 关闭")
	return nil
}

// 包级 handler 转发给实例方法（plugin Routes 协议要求顶层 handler 函数）
func handleSpecWrite(w http.ResponseWriter, r *http.Request)  { Plugin.handleSpecWrite(w, r) }
func handleSpecDelete(w http.ResponseWriter, r *http.Request) { Plugin.handleSpecDelete(w, r) }
func handleSpecGet(w http.ResponseWriter, r *http.Request)    { Plugin.handleSpecGet(w, r) }
func handleSpecList(w http.ResponseWriter, r *http.Request)   { Plugin.handleSpecList(w, r) }
func handleMenu(w http.ResponseWriter, r *http.Request)       { Plugin.handleMenu(w, r) }

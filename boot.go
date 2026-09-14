// boot.go 浏览器版与桌面版共用的启动装配：配置 → 日志 → LLM 网关 → 引擎 → 存档 → HTTP 路由。
// 拆出来的原因：main.go（浏览器版）与 main_wails.go（桌面版）除此以外只差"谁来展示窗口"。
package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"aitown/internal/config"
	"aitown/internal/game"
	"aitown/internal/llm"
	"aitown/internal/server"
	"aitown/internal/store"
	"aitown/internal/xlog"
)

// version 构建版本：release 打包时由 -ldflags "-X main.version=vX.Y.Z" 注入，开发构建为 dev。
var version = "dev"

// appParts 一组完成装配的运行时组件。
type appParts struct {
	eng     *game.Engine
	gw      *llm.Gateway
	hub     *server.Hub
	cfg     *config.Config
	cfgPath string
	handler http.Handler
	db      *store.DB
}

// boot 按数据目录装配全部运行时：配置/日志/引擎/存档库（自动读档）/HTTP 路由。
// 返回时世界线程已在运行；调用方负责 HTTP 服务与退出时调用 close()。
func boot(dataDir, mode string) (*appParts, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	cfgPath := filepath.Join(dataDir, "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}

	// 运维诊断日志：stderr + <data>/logs/aitown.log（5MB 轮转）；debug 开关存配置
	if err := xlog.Setup(dataDir, cfg.Debug); err != nil {
		return nil, fmt.Errorf("初始化日志失败: %w", err)
	}
	xlog.Info("AI 领地启动", "version", version, "mode", mode, "llm", mockHint(cfg), "debug", cfg.Debug)

	gw := llm.NewGateway(cfg)
	hub := server.NewHub()
	eng := game.NewEngine(gw, hub)
	ap := &appParts{eng: eng, gw: gw, hub: hub, cfg: cfg, cfgPath: cfgPath}

	// 持久化：存档库打开失败不致命（退化为纯内存运行），但会在日志里明确告知
	if db, err := store.Open(filepath.Join(dataDir, "saves.db")); err != nil {
		xlog.Warn("存档库打开失败，本次运行不落盘", "err", err)
	} else {
		ap.db = db
		eng.AttachSaver(db)
		// 不自动读档：存档概况进 GET /api/state，由前端"继续 / 新世界"选择页决定
		// （继续走 POST /api/continue；开新世界会立即用新世界覆盖旧档）
		if meta := eng.SaveMeta(); meta.Exists {
			xlog.Info("发现存档，等待玩家选择继续或新世界", "world", meta.WorldName, "day", meta.Day)
		}
	}
	eng.Start()

	srv := server.New(eng, gw, cfg, cfgPath, webDist(), hub)
	ap.handler = srv.Handler()
	return ap, nil
}

// close 退出清理：退出存档 + 停世界线程与村民 goroutine + 冲刷日志 + 关存档库。
func (a *appParts) close() {
	a.eng.Stop()
	xlog.Sync() // 优雅退出时把异步日志落盘（桌面版关窗走这条路径）
	if a.db != nil {
		_ = a.db.Close()
	}
}

// mockHint 启动横幅/日志用的 LLM 模式提示。
func mockHint(cfg *config.Config) string {
	p := cfg.Providers["default"]
	if p != nil && p.Type == config.ProviderMock {
		return "mock 演示（不消耗 token）"
	}
	return "BYOK 已接入"
}

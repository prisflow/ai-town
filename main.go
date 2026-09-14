//go:build !wails

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	// 接收和处理系统信号
	"os/signal"
)

// main 浏览器版入口：装配运行时（boot）后监听 HTTP，让玩家用浏览器访问。
// 桌面版（Wails 窗口壳）在 main_wails.go（-tags wails），两者共用 boot.go。
func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "监听地址")
	dataDir := flag.String("data", "data", "数据目录（BYOK 配置/存档/日志）")
	flag.Parse()

	ap, err := boot(*dataDir, "browser")
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	defer ap.close()

	// Ctrl+C 优雅退出：退出存档 + 日志落盘（直接关控制台窗口不会走这里，日界自动存档兜底）
	sig := make(chan os.Signal, 1)
	// 监听 Ctrl+c
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		ap.close()
		os.Exit(0)
	}()

	fmt.Printf(`
  ╭──────────────────────────────────────────────╮
  │   AI 领地  ·  ai-town                        │
  │   打开浏览器访问  http://%s  │
  │   当前 LLM 模式：%s（在「设置」里配置 BYOK）  │
  ╰──────────────────────────────────────────────╯
  版本：%s
`, *addr, mockHint(ap.cfg), version)

	if err := http.ListenAndServe(*addr, ap.handler); err != nil {
		log.Fatalf("HTTP 服务退出: %v", err)
	}
}

//go:build wails

// main_wails.go 桌面版入口：Wails v2 只当"窗口壳"，游戏本体仍跑在真实 HTTP 上。
//
// 【为什么绕一圈用 bootstrap 跳转】
// 实测（wails 2.15 spike）：Wails 的 AssetServer 能服务静态页与 POST，但它的
// ResponseWriter 不支持 http.Flusher——SSE（EventSource）流式推送直接失败，
// 而前端依赖 250ms 快照流。因此：窗口先加载一个自包含 bootstrap 页，页面
// location.replace 到 127.0.0.1 的动态端口真实 HTTP 服务；此后 fetch/SSE 全走
// 正常网络栈，前端零改动，Wails 只负责窗口、图标与生命期。
//
// 构建：npm run build（web）→ wails build -s -clean（-s 跳过 Wails 的前端构建）
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing/fstest"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"aitown/internal/xlog"
)

func main() {
	dataDir := flag.String("data", "", `数据目录（默认 %AppData%\aitown）`)
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			log.Fatalf("无法确定用户配置目录: %v", err)
		}
		dir = filepath.Join(base, "aitown")
	}

	ap, err := boot(dir, "desktop")
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}

	// 真实 HTTP 服务：绑 127.0.0.1 随机端口（仅本机可达，不占固定端口）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ap.close()
		log.Fatalf("监听失败: %v", err)
	}
	appURL := fmt.Sprintf("http://%s/", ln.Addr().String())
	handler := ap.handler
	// 只有环境变量为1的时候，才会用日志包装一层
	if os.Getenv("AITOWN_ACCESS_LOG") == "1" { // 桌面版排障：记录每个 HTTP 请求（直写文件，进程被强杀也不丢）
		handler = accessLog(handler, dir)
	}
	go func() {
		if serr := http.Serve(ln, handler); serr != nil {
			log.Printf("HTTP 服务退出: %v", serr)
		}
	}()
	xlog.Info("桌面版就绪", "url", appURL, "data", dir)

	// bootstrap 页：尽快把窗口交给真实 HTTP 服务；JS 被禁时给出可点的兜底链接
	// Wails侧不承载真正的前端
	boot := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>AI 领地</title></head>
<body style="background:#0b1220;color:#94a3b8;font-family:system-ui;display:flex;align-items:center;justify-content:center;height:100vh;margin:0">
<div style="text-align:center">正在进入领地…<br><a style="color:#38bdf8" href="%s">若未自动跳转，点这里</a></div>
<script>location.replace('%s');</script></body></html>`, appURL, appURL)

	err = wails.Run(&options.App{
		Title:     "AI 领地 · ai-town",
		Width:     1360,
		Height:    860,
		MinWidth:  1024,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(boot)}},
		},
		OnShutdown: func(_ context.Context) { ap.close() },
		Windows: &windows.Options{
			WebviewUserDataPath: filepath.Join(dir, "webview"), // WebView2 缓存放进应用数据目录
		},
	})
	if err != nil {
		ap.close()
		log.Fatalf("窗口退出: %v", err)
	}
}

// HTTP中间件
// accessLog 请求访问日志：追加到 <data>/access.log（仅 AITOWN_ACCESS_LOG=1 时启用）。
// 直写文件（无缓冲），进程被强杀也能看到最后一条请求，用于诊断"窗口是否真的连上后端"。
func accessLog(next http.Handler, dataDir string) http.Handler {
	// 不存在就创建|写入为追加|只写 
	// 0o644 所有者读写，其他人只读
	f, err := os.OpenFile(filepath.Join(dataDir, "access.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		xlog.Warn("访问日志打开失败", "err", err)
		// 无参数返回，直接跳过日志进行下一个handler
		return next
	}
	// 进来走这个闭包，先写日志，再跑业务
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(f, "%s %s %s\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path)
		// 传递请求
		next.ServeHTTP(w, r)
	})
}

// AI 领地（ai-town）：2D 像素模拟领主游戏。
// 多智能体并发工作：智能体持有 workflow 封装的行为，BYOK 直连你自己的 LLM。
//
// # 文件地图
//
//	main.go       浏览器版入口：boot 装配后监听 HTTP，玩家用浏览器访问
//	main_wails.go 桌面版入口（-tags wails）：Wails 窗口壳 + bootstrap 跳转到 loopback HTTP
//	boot.go       两版共用的启动装配（配置/日志/引擎/存档/路由）
//	webfs.go      前端构建产物内嵌（go:embed web/dist），最终交付只有一个 exe
//	doc.go        本文件
//
// # 构建
//
//	cd web && npm run build        （先出前端产物）
//	go build -o aitown.exe .       （浏览器版单文件）
//	wails build -s -clean          （桌面版单文件，需 wails CLI v2）
//	./aitown.exe                   （默认 127.0.0.1:8080，-addr/-data 可调）
//
// # 各层在哪
//
//	internal/game      游戏业务（阅读入口：internal/game/doc.go）
//	internal/sim       确定性世界物理
//	internal/workflow  JSON 行为模板解释器
//	internal/llm       BYOK 网关
//	internal/worldgen  LLM 建世界
//	internal/server    HTTP + SSE
//	internal/config    BYOK 配置
//	internal/xlog      诊断日志
//	cmd/docgen         文档生成器（开发工具，不进二进制）
package main

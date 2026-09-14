// Package xlog 极简运维诊断日志：slog 双输出（stderr + 轮转文件）、
// 同 key 节流（防错误风暴）、环形缓冲 tail（供 /api/diag）、trace 上下文传播。
// 零内部依赖；玩家叙事事件流（publishEvent）与之严格分离。
//
// # 文件地图（单文件包）
//
//	xlog.go  Setup（stderr 同步 + 文件异步轮转 5MB×2）、级别与 Debug 开关、
//	         Info/Warn/Error/Debug、WarnEvery/ErrorEvery（节流）、
//	         Tail（诊断面板数据源）、WithTrace/TraceFrom（全链路追踪）、Trunc
//
// # 怎么读
//
//	Setup → 包级日志函数 → WarnEvery（为什么需要节流：村民并发失败会刷屏）。
//
// # 使用约定
//
//	诊断/运维日志走本包；给玩家看的事件走 game.publishEvent（走 SSE）。
//	trace 用 xlog.WithTrace(ctx, "a92-7") 注入，贯穿 agent→brain→gateway 日志。
package xlog

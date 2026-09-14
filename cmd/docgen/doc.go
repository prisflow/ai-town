// cmd/docgen 文档生成器（仅开发工具，不进运行时二进制）。
//
// 以 Go 结构体为单一事实源，生成四类产物：
//
//	docs/PROTOCOL.md      SSE 帧协议 + REST 端点参考（字段表自动生成）
//	docs/DATA-MODEL.md    领域数据模型参考（sim/workflow/game/config/llm）
//	docs/schemas/*.json   关键消息的 JSON Schema（机器可校验）
//	web/src/api.gen.ts    前端 TS 类型（api.ts 引用，防前后端协议漂移）
//
// 字段说明直接取自结构体字段上方的 Go 注释（经 go/parser 提取）。
// 协议或模型变更后运行：go run ./cmd/docgen
//
// # 文件地图（单文件工具）
//
//	main.go  收集器（go/parser 提取结构体与注释）+ 四类产物生成器，按"产物"分段
//	doc.go   本文件
package main

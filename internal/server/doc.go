// Package server 提供 HTTP API（REST + SSE）与前端静态资源服务。
//
// # 文件地图
//
//	hub.go     SSE 订阅管理：把 game 层消息序列化后扇出给所有浏览器连接；
//	           慢消费者丢帧（前端靠 250ms 快照流自愈），不阻塞游戏循环
//	server.go  路由与 handler：/api/state|world|edict|pause|official|exile|
//	           config|config/test|diag|health + SPA 静态回退 + CORS + JSON 帮助函数
//
// # 怎么读
//
//	hub.go（先懂推流，再懂接口）→ server.go 的 Handler() 路由表。
//
// # 一条边界
//
//	HTTP handler 是独立 goroutine：所有世界读写都经 game.Engine 的锁内 API，
//	不直接碰 sim.World；SSE 推送只做序列化扇出。
package server

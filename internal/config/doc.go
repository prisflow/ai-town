// Package config 管理 AI 领地的运行时配置：BYOK 提供商、角色到提供商的路由、全局限额。
// 配置持久化在 data/config.json（默认），密钥只落本地盘，不经任何第三方。
//
// # 文件地图（单文件包）
//
//	config.go  ProviderConfig / LimitsConfig / PacingConfig / Config 数据结构、
//	           Defaults / Load / Save、Masked（脱敏后给前端）、
//	           ProviderForRole（角色路由解析）、sanitize（非法值回退与钳位）
//
// # 怎么读
//
//	Config 结构体（字段注释即文档）→ Defaults → ProviderForRole；
//	节奏类参数看 PacingConfig（设置页"节奏"分组的热更源）。
package config

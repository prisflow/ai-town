// Package worldgen 实现"世界初始化 workflow"：
// 用户自然语言需求 → LLM 生成世界规格（名称/传说/村民阵容）→ 确定性算法摆放地图。
// LLM 失败时回退到内置默认世界，保证任何情况下都能开局。
//
// # 文件地图
//
//	spec.go   Spec / Villager 数据、Normalize（非法输入回退）、
//	          DefaultSpec（内置"迷雾河谷"）
//	build.go  BuildWorld(spec, seed, size)：确定性建图
//	          （湖与沙岸 → 十字主路 → 领主堡/民居/粮仓/农田 → 资源点位 → 角色落位）
//
// # 怎么读
//
//	spec.go（先看数据结构与 Normalize 的兜底）→ build.go 的 BuildWorld 主流程。
//
// # 不变量
//
//	同 (spec, seed, size) 输出逐字节一致——有测试保证（见 season/build 相关用例）。
package worldgen

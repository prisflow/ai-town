// Package workflow 是行为封装层：JSON 模板 + 阻塞式解释器。
//
// 设计原则（与智能体严格分离）：
//   - 模板（Template）是数据：一段 JSON 行为图，由确定性原语步骤（移动/采集/交付）
//     和少量 AI 决策节点（ai_choice / ai say）组成，可由用户自行编写与扩展；
//   - 智能体（game 包的 agent goroutine）是决策者：决定何时启动哪个模板实例并注入参数；
//   - 解释器按顺序执行步骤，步骤内部通过 Host 接口阻塞到完成（走到/采完/聊完），
//     因此 workflow 包本身是纯同步代码，不依赖 sim/game/llm，保持纯粹可测试。
//
// # 文件地图
//
//	template.go      数据结构与接口：Template / StepSpec、Host 接口、CheckSpec、
//	                 Engine（模板注册 / LoadBuiltin 加载内置）
//	run.go           Run（一次运行）：Start 创建 / Execute 顺序执行 / 失败原因 / 前插分支
//	steps.go         全部步骤实现：goto / goto_nearest / wander / work / deposit / withdraw /
//	                 eat / wait / say / talk / sleep / condition / ai_choice
//	templates/*.json 15 个内置行为模板（go:embed 打包）
//
// # 怎么读
//
//	template.go（Host 接口是理解边界的关键）→ run.go（Execute 主干只有 25 行）
//	→ steps.go 对照 templates/*.json 按需查。
//
// # 等待与兜底
//
//	本包不做等待实现：走到/干完/聊完的"怎么等"由 Host 实现负责
//	（game.agentHost 用 waitWorld 双通道：世界事实轮询 + 事件快速唤醒）。
//	步骤返回错误即工作流失败，向上冒泡给 agent 的失败退避。
package workflow

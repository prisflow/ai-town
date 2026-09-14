// Package llm 是 BYOK 的 LLM 网关：多提供商客户端、角色路由、并发/频率限额、token 统计。
// 设计原则：仿真主循环是单线程的，LLM 调用全部异步完成，结果经游戏循环的邮箱串行回流。
//
// # 文件地图
//
//	gateway.go   Gateway：Request / Response / Stats、Complete / CompleteAsync、
//	             角色→提供商路由（RoleWorldgen/Planner/Brain/Dialogue/Choice/Narrate）、
//	             RPM 限流与并发信号量、错误记录
//	openai.go    OpenAI 兼容客户端（覆盖 DeepSeek/Moonshot/Qwen/智谱/Ollama/OpenRouter…）
//	anthropic.go Anthropic Messages API 客户端
//	mock.go      确定性 mock（离线全流程 + 所有测试）
//	jsonutil.go  ExtractJSON / ParseData：容忍代码块、前后缀文本、截断的 JSON 解析
//
// # 怎么读
//
//	gateway.go（Complete 是唯一咽喉点，日志/统计/限流都在那里）→ 客户端按需看；
//	解析报错时再看 jsonutil.go。
//
// # 两条约定
//
//   - 所有调用都必须带角色（Role*）：换模型只改 config 的角色路由，代码零改动。
//   - 游戏层全走 CompleteAsync + 邮箱回流；Complete 只用于 workflow 步骤等阻塞场景。
package llm

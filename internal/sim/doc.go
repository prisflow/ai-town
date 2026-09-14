// Package sim 是确定性世界仿真核心：瓦片地图、实体、库存、时间、移动与劳作结算。
// 该包不含任何 AI 逻辑，只暴露同步的机制原语，由上层 game 包在单一 goroutine 中驱动，
// 从而保证：整个仿真无锁、无数据竞争、可被测试逐 tick 复现。
//
// # 文件地图
//
//	sim.go    类型与常量：Tile / ResourceKind / BuildingKind / BusyKind / Season /
//	          WorkState / Actor（物理实体：位置/路径/背包/劳作）
//	world.go  World 本体：时间（Day/Hour/Season/IsNight）、地图查询（Passable/BuildingAt）、
//	          库存（AddRes/Withdraw/DepositAll/EatMeal/ResCaps）、软认领（ClaimRes/ReleaseClaims）、
//	          物理结算（StepActorMove / StepActorWork / CancelActor）、连通性（ConnectivityOK）
//	path.go   A* 寻路（FindPath，四向）
//
// # 怎么读
//
//	sim.go（先认类型）→ world.go 的 NewWorld / Day / Hour / IsNight / StepActor*
//	（结算入口）→ 其余按需查；path.go 最后看（算法细节不影响理解）。
//
// # 两条不变量（改动前必知）
//
//   - 纯数据 + 方法，包内无锁、无 goroutine：调用方负责持锁（game 包用 e.mu 大锁）。
//   - 时间全部由 Tick 推导（Day/Hour/Season/IsNight 都是纯函数）：
//     TicksPerHour=150、TicksPerDay=3600，配合引擎"10 tick/秒"的节拍 = 6 分钟一游戏天。
package sim

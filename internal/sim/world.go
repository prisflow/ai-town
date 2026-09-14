package sim

import (
	"fmt"
	"math"
	"math/rand"
)

// TicksPerHour 1 游戏小时 = 150 tick（10 tick/秒 时约 15 秒），1 天 = 6 分钟。
const TicksPerHour = 150

// TicksPerDay 一整天的 tick 数。
const TicksPerDay = TicksPerHour * 24

// World 世界状态：地图、实体、领地库存、时间。仅在游戏循环单线程内读写。
type World struct {
	// Name 领地名称（世界初始化时由 LLM 生成，失败回退默认"迷雾河谷"）。
	Name string
	// Lore 世界传说/背景故事，纯展示用途（新建世界卡片、规划器上下文）。
	Lore string

	// W, H 地图宽高（格数）。
	W, H int
	// Tiles 瓦片地形数组，行优先：idx = y*W+x。决定通行性与渲染底图。
	Tiles []Tile
	// Res 资源节点表（树/浆果丛/岩石），key = 资源 ID。
	Res map[string]*Resource
	// ResOrd 资源 ID 的注册顺序；map 遍历无序，靠它保证确定性与 JSON 输出稳定。
	ResOrd []string
	// Bld 建筑表（领主堡/民居/粮仓/农田，含未完工工地），key = 建筑 ID。
	Bld map[string]*Building
	// BldOrd 建筑 ID 的注册顺序（作用同 ResOrd）。
	BldOrd []string
	// BGrid 瓦片→建筑 ID 的空间索引：每格记录压在其上的建筑 ID，空串 = 无建筑。
	// O(1) 回答"这格能不能走 / 属于哪个建筑"，AddBuilding 时填充。
	BGrid []string
	// Actors 居民实体表（位置/朝向/移动路径/劳作状态/背包），key = 居民 ID。
	// 注意：这里只是"物理存在"，村民的记忆/性格等大脑状态在 game.Citizen。
	Actors map[string]*Actor
	// ActOrd 居民 ID 的注册顺序；每 tick 按此顺序推进所有人（确定性）。
	ActOrd []string

	// Inventory 领地公共库存（wood/food/stone）：采集交付入库、建造/吃饭从此扣减。
	Inventory map[string]int

	// Tick 世界累计 tick 数，即逻辑时钟：
	// 10 tick = 1 真实秒(1x)，150 tick = 1 游戏小时，3600 tick = 1 游戏天。
	// 劳作耗时、资源再生、日夜判定全部以此为唯一时间基准。
	Tick int64
	// Paused 暂停标志：true 时 Tick 停走、居民与劳作全部冻结（SSE 快照仍照常发送）。
	Paused bool
	// NightStart, NightEnd 夜间区间（24 小时制）：NightStart 后或 NightEnd 前视为夜里。
	// 由 game 层在创建世界时从节奏配置注入；默认 21.5 / 6。
	NightStart, NightEnd float64

	// ResCaps 各资源的仓库独立容量（件）。共享总容量会被单一资源（如小麦）占满，
	// 挤死其他资源的入库（实测：小麦爆仓后浆果 18 分钟交不进去）——因此分资源限额。
	ResCaps map[string]int

	// Seed 世界随机源种子：rng 不可序列化，存档时带种子、读档按种子重建随机源。
	Seed int64

	// rng 世界级随机源（种子化）：地形生成、散步落点、日常骰子共用。
	// 因只在游戏循环单线程内使用，无需加锁；同 seed 可完整复现世界。
	rng *rand.Rand
	// nextID ID 发号器：GenID 据此生成 r1/b1/a1 等自增实体 ID。
	nextID int

	// claims 资源软认领表：resID → actorID。引导村民分散采集、缓解"全员挤一棵树"
	// 的竞态（无强制：全被认领时允许共用）。仅世界线程/持引擎锁环境读写。
	claims map[string]string
}

// baseResCaps 各资源的基础容量；粮仓在其上追加储粮加成（见 RecalcInvCap）。
var baseResCaps = map[string]int{
	"wood": 120, "food": 120, "stone": 90,
	"wheat": 150, "flour": 60, "bread": 60,
}

// NewWorld 创建空白世界。
func NewWorld(w, h int, seed int64) *World {
	world := &World{
		W: w, H: h,
		Tiles:      make([]Tile, w*h),
		Res:        map[string]*Resource{},
		Bld:        map[string]*Building{},
		Actors:     map[string]*Actor{},
		Inventory:  map[string]int{"wood": 10, "food": 15, "stone": 4},
		rng:        rand.New(rand.NewSource(seed)),
		Seed:       seed,
		Tick:       TicksPerHour * 7, // 从清晨 7 点开局，居民立刻活跃
		NightStart: 21.5, NightEnd: 6,
	}
	world.BGrid = make([]string, w*h)
	world.claims = map[string]string{}
	world.RecalcInvCap()
	return world
}

// ClaimRes 软认领资源：未被认领或已是自己认领的返回 true。
func (w *World) ClaimRes(actorID, resID string) bool {
	if owner, ok := w.claims[resID]; ok && owner != actorID {
		return false
	}
	w.claims[resID] = actorID
	return true
}

// ResClaimedBy 返回资源当前认领者（"" = 未认领）。
func (w *World) ResClaimedBy(resID string) string { return w.claims[resID] }

// ReleaseClaims 释放某居民的全部认领（workflow 收尾/被打断时调用）。
func (w *World) ReleaseClaims(actorID string) {
	for id, owner := range w.claims {
		if owner == actorID {
			delete(w.claims, id)
		}
	}
}

// RNG 暴露世界随机源（仅限游戏循环内使用，保证确定性）。
func (w *World) RNG() *rand.Rand { return w.rng }

// GenID 生成实体 ID。
func (w *World) GenID(prefix string) string {
	w.nextID++
	return fmt.Sprintf("%s%d", prefix, w.nextID)
}

// Day 当前第几天（从 1 起）。
func (w *World) Day() int { return int(w.Tick/TicksPerDay) + 1 }

// Season 当前季节：第 n 天 → (n-1)/7 % 4（春/夏/秋/冬循环）。
func (w *World) Season() Season {
	return Season(((w.Day() - 1) / DaysPerSeason) % 4)
}

// Hour 当前时刻 0-24（浮点）。
func (w *World) Hour() float64 { return float64(w.Tick%TicksPerDay) / TicksPerHour }

// SeasonMoveMul 冬季移动 ×0.9（天寒地冻），其余季节 ×1。
func (w *World) SeasonMoveMul() float64 {
	if w.Season() == SeasonWinter {
		return 0.9
	}
	return 1
}

// seasonBerryBonus 秋收/春耕的浆果额外产出概率（采集 1 单位时如概率多拿 1 单位）。
func (w *World) seasonBerryBonus() bool {
	switch w.Season() {
	case SeasonAutumn:
		return w.rng.Float64() < 0.5
	case SeasonSpring:
		return w.rng.Float64() < 0.25
	}
	return false
}

// RecalcInvCap 重算各资源容量：基础值 + 每座建成粮仓的储粮加成。建筑变化后调用。
func (w *World) RecalcInvCap() {
	caps := make(map[string]int, len(baseResCaps))
	for k, v := range baseResCaps {
		caps[k] = v
	}
	granaries := 0
	for _, id := range w.BldOrd {
		if b := w.Bld[id]; b.Kind == BGranary && b.Complete {
			granaries++
		}
	}
	if granaries > 0 {
		caps["wheat"] += 100 * granaries
		caps["flour"] += 50 * granaries
		caps["bread"] += 50 * granaries
		caps["food"] += 30 * granaries
	}
	w.ResCaps = caps
}

// AddRes 增加库存（受该资源容量上限约束），返回实际增加量。
func (w *World) AddRes(res string, n int) int {
	space := w.ResCaps[res] - w.Inventory[res]
	if space <= 0 {
		return 0
	}
	if n > space {
		n = space
	}
	w.Inventory[res] += n
	return n
}

// ResFull 该资源是否已满仓。
func (w *World) ResFull(res string) bool {
	return w.Inventory[res] >= w.ResCaps[res]
}

// ConnectivityOK 连通性自检（选址用）：全体村民必须处于同一连通区；
// 领主堡尚有可通行临格时，连通区必须能接触到堡本身。
// 防止新建筑把村民或城堡围死（实测：连着建农田把村民封印、无法去磨坊/吃饭）。
func (w *World) ConnectivityOK() bool {
	if len(w.ActOrd) == 0 {
		return true
	}
	start := w.Actors[w.ActOrd[0]]
	if start == nil {
		return true
	}
	sx, sy := int(start.X), int(start.Y)
	if !w.Passable(sx, sy) {
		t, ok := w.NearestFreeAround(sx, sy)
		if !ok {
			return true // 起点异常：不判罚
		}
		sx, sy = t.X, t.Y
	}
	seen := map[Pt]bool{{X: sx, Y: sy}: true}
	queue := []Pt{{X: sx, Y: sy}}
	touchesKeep := false
	keep := w.Keep()
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if keep != nil {
			nx := clamp(p.X, keep.X, keep.X+keep.W-1)
			ny := clamp(p.Y, keep.Y, keep.Y+keep.H-1)
			if abs(p.X-nx)+abs(p.Y-ny) <= 1 {
				touchesKeep = true
			}
		}
		for _, d := range dirs4 {
			n := Pt{X: p.X + d[0], Y: p.Y + d[1]}
			if seen[n] || !w.Passable(n.X, n.Y) {
				continue
			}
			seen[n] = true
			queue = append(queue, n)
		}
	}
	// 全体村民必须都在连通区内（站位异常时放宽到最近可行格判断）
	for _, id := range w.ActOrd {
		act := w.Actors[id]
		if act == nil {
			continue
		}
		p := Pt{X: int(act.X), Y: int(act.Y)}
		if seen[p] {
			continue
		}
		if t, ok := w.NearestFreeAround(p.X, p.Y); ok && seen[t] {
			continue
		}
		return false
	}
	// 领主堡还有可通行临格时：连通区必须能碰到堡（防止把堡围死）
	if keep != nil && w.keepHasFreeSide(keep) && !touchesKeep {
		return false
	}
	return true
}

// keepHasFreeSide 领主堡周围是否还有可通行的临格。
func (w *World) keepHasFreeSide(b *Building) bool {
	for x := b.X; x < b.X+b.W; x++ {
		if w.Passable(x, b.Y-1) || w.Passable(x, b.Y+b.H) {
			return true
		}
	}
	for y := b.Y; y < b.Y+b.H; y++ {
		if w.Passable(b.X-1, y) || w.Passable(b.X+b.W, y) {
			return true
		}
	}
	return false
}

// IsNight 是否夜间（默认 21:30 后或 6 点前；区间可由 game 层注入）。
func (w *World) IsNight() bool {
	h := w.Hour()
	start, end := w.NightStart, w.NightEnd
	if start <= 0 || start > 24 {
		start = 21.5
	}
	if end < 0 || end >= 24 {
		end = 6
	}
	return h >= start || h < end
}

// InBounds 坐标是否在界内。
func (w *World) InBounds(x, y int) bool { return x >= 0 && y >= 0 && x < w.W && y < w.H }

// TileAt 读取瓦片。
func (w *World) TileAt(x, y int) Tile {
	if !w.InBounds(x, y) {
		return Water
	}
	return w.Tiles[y*w.W+x]
}

// SetTile 写瓦片。
func (w *World) SetTile(x, y int, t Tile) {
	if w.InBounds(x, y) {
		w.Tiles[y*w.W+x] = t
	}
}

// BuildingAt 返回压在该瓦片上的建筑（可 nil）。
func (w *World) BuildingAt(x, y int) *Building {
	if !w.InBounds(x, y) {
		return nil
	}
	if id := w.BGrid[y*w.W+x]; id != "" {
		return w.Bld[id]
	}
	return nil
}

// Passable 瓦片级通行判定：地形 + 无建筑覆盖。
func (w *World) Passable(x, y int) bool {
	if !w.InBounds(x, y) {
		return false
	}
	if !w.Tiles[y*w.W+x].Walkable() {
		return false
	}
	return w.BGrid[y*w.W+x] == ""
}

// AddBuilding 登记建筑并占用网格。
func (w *World) AddBuilding(b *Building) {
	w.Bld[b.ID] = b
	w.BldOrd = append(w.BldOrd, b.ID)
	for y := b.Y; y < b.Y+b.H; y++ {
		for x := b.X; x < b.X+b.W; x++ {
			if w.InBounds(x, y) {
				w.BGrid[y*w.W+x] = b.ID
			}
		}
	}
}

// RemoveBuilding 移除建筑并释放网格（撤销未完工蓝图用）。
func (w *World) RemoveBuilding(id string) {
	b := w.Bld[id]
	if b == nil {
		return
	}
	delete(w.Bld, id)
	for i, v := range w.BldOrd {
		if v == id {
			w.BldOrd = append(w.BldOrd[:i], w.BldOrd[i+1:]...)
			break
		}
	}
	for y := b.Y; y < b.Y+b.H; y++ {
		for x := b.X; x < b.X+b.W; x++ {
			if w.InBounds(x, y) && w.BGrid[y*w.W+x] == id {
				w.BGrid[y*w.W+x] = ""
			}
		}
	}
}

// AddResource 登记资源节点。
func (w *World) AddResource(r *Resource) {
	w.Res[r.ID] = r
	w.ResOrd = append(w.ResOrd, r.ID)
}

// AddActor 登记居民实体。
func (w *World) AddActor(a *Actor) {
	w.Actors[a.ID] = a
	w.ActOrd = append(w.ActOrd, a.ID)
}

// RemoveActor 注销居民实体。
func (w *World) RemoveActor(id string) {
	delete(w.Actors, id)
	for i, o := range w.ActOrd {
		if o == id {
			w.ActOrd = append(w.ActOrd[:i], w.ActOrd[i+1:]...)
			break
		}
	}
}

// RegenResources 恢复到期的资源节点。冬季万物休眠：不再生（开春后一次性恢复）。
func (w *World) RegenResources() {
	if w.Season() == SeasonWinter {
		return
	}
	for _, r := range w.Res {
		if r.Depleted && r.RegrowAt > 0 && w.Tick >= r.RegrowAt {
			r.Depleted = false
			r.Amount = r.Max
			r.RegrowAt = 0
		}
	}
}

// StepActorMove 推进移动，返回是否到达（路径走空）。
func (w *World) StepActorMove(a *Actor, dt float64) bool {
	if a.Busy != BusyMove {
		return true
	}
	remain := a.Speed * dt
	for remain > 1e-9 && len(a.Path) > 0 {
		t := a.Path[0]
		tx, ty := float64(t.X)+0.5, float64(t.Y)+0.5
		dx, dy := tx-a.X, ty-a.Y
		d := math.Hypot(dx, dy)
		if d <= remain {
			a.X, a.Y = tx, ty
			remain -= d
			w.applyFacing(a, dx, dy)
			a.Path = a.Path[1:]
		} else {
			a.X += dx / d * remain
			a.Y += dy / d * remain
			w.applyFacing(a, dx, dy)
			remain = 0
		}
	}
	if len(a.Path) == 0 {
		a.Busy = BusyNone
		return true
	}
	return false
}

func (w *World) applyFacing(a *Actor, dx, dy float64) {
	if math.Abs(dx) > math.Abs(dy) {
		if dx > 0 {
			a.Facing = 3
		} else if dx < 0 {
			a.Facing = 2
		}
	} else if math.Abs(dy) > 1e-9 {
		if dy > 0 {
			a.Facing = 0
		} else {
			a.Facing = 1
		}
	}
}

// BeginMove 让居民沿路径移动。空路径视为"已在原地"，下一 tick 立即回报到达，
// 否则 goto 步骤会永远等不到 arrived 事件（已在目标点旁的常见情形）。
func (w *World) BeginMove(a *Actor, path []Pt) {
	a.Path = path
	a.Busy = BusyMove
}

// BeginWork 开始劳作；kind: harvest/tend/build；maxUnits: tend 类工作的单位数上限，-1 不限。
func (w *World) BeginWork(a *Actor, kind, targetID string, maxUnits int) {
	a.Busy = BusyWork
	a.Work = &WorkState{Kind: kind, TargetID: targetID, NextUnitTick: w.Tick, MaxUnits: maxUnits}
}

// CancelActor 打断后的物理清理：清移动路径与劳作状态（背包保留）。
// 对话中的村民不动（BusyTalk 的物理由对话系统权威管理）。
// 需持锁，仅世界线程/持引擎锁环境调用。
func (w *World) CancelActor(actorID string) {
	a := w.Actors[actorID]
	if a == nil || a.Busy == BusyTalk {
		return
	}
	a.Busy = BusyNone
	a.Work = nil
	a.Path = nil
}

// AdjacentToTile 居民是否与目标瓦片相邻（含自身所在）。
func (w *World) AdjacentToTile(a *Actor, x, y int) bool {
	ax, ay := int(a.X), int(a.Y)
	return abs(ax-x) <= 1 && abs(ay-y) <= 1
}

// AdjacentToBuilding 居民是否与建筑相邻。
func (w *World) AdjacentToBuilding(a *Actor, b *Building) bool {
	ax, ay := int(a.X), int(a.Y)
	nx := clamp(ax, b.X, b.X+b.W-1)
	ny := clamp(ay, b.Y, b.Y+b.H-1)
	return abs(ax-nx)+abs(ay-ny) <= 1
}

// StepActorWork 推进一格劳作单位，返回 (是否完成, 结果标记)。
// 结果标记：full(仓容已满) empty(资源枯竭/原料不足) built(建成) tended(劳作结束)。冬季农田休耕。
func (w *World) StepActorWork(a *Actor) (bool, string) {
	ws := a.Work
	if ws == nil {
		return true, ""
	}
	if w.Tick < ws.NextUnitTick {
		return false, ""
	}
	interval := int64(8)
	if ws.Kind == WorkTend {
		// 农活慢工出细活：1.2 秒/单位（其余劳作节奏不变）
		interval = int64(12)
	}
	switch ws.Kind {
	case WorkHarvest:
		r := w.Res[ws.TargetID]
		if r == nil || r.Depleted || r.Amount <= 0 {
			return true, "empty"
		}
		res := r.Kind.Yield()
		r.Amount--
		a.Carrying[res]++
		ws.Units++
		// 季节加成：春/秋采集浆果有概率多得 1 单位
		if r.Kind == ResBerry && w.seasonBerryBonus() {
			a.Carrying[res]++
		}
		if r.Amount <= 0 {
			r.Depleted = true
			r.RegrowAt = w.Tick + TicksPerHour*20
			delete(w.claims, ws.TargetID) // 枯竭即释放认领
			return true, "empty"          // 采空即收工，不空等一个劳作间隔
		}
		ws.NextUnitTick = w.Tick + int64(r.Kind.WorkSeconds()*10)
		return false, ""
	case WorkTend:
		if w.Season() == SeasonWinter {
			return true, "empty" // 冬季农田休耕
		}
		if w.ResFull("wheat") {
			return true, "full" // 粮仓已满：收工（需求门控会让农夫改干别的）
		}
		w.AddRes("wheat", 1) // 每单位 +1：配合独立上限，产出不再无限膨胀
		ws.Units++
		ws.NextUnitTick = w.Tick + interval
		if ws.MaxUnits > 0 && ws.Units >= ws.MaxUnits {
			return true, "tended"
		}
		return false, ""
	case WorkGrind:
		if w.Inventory["wheat"] < 2 {
			return true, "empty"
		}
		if w.ResFull("flour") {
			return true, "full" // 面粉仓已满
		}
		w.Inventory["wheat"] -= 2
		w.AddRes("flour", 1)
		ws.Units++
		ws.NextUnitTick = w.Tick + interval
		if ws.MaxUnits > 0 && ws.Units >= ws.MaxUnits {
			return true, "tended"
		}
		return false, ""
	case WorkBake:
		if w.Inventory["flour"] < 1 || w.Inventory["food"] < 2 {
			return true, "empty"
		}
		if w.ResFull("bread") {
			return true, "full" // 面包架已满
		}
		w.Inventory["flour"]--
		w.Inventory["food"] -= 2
		w.AddRes("bread", 1)
		ws.Units++
		ws.NextUnitTick = w.Tick + interval
		if ws.MaxUnits > 0 && ws.Units >= ws.MaxUnits {
			return true, "tended"
		}
		return false, ""
	case WorkBuild:
		b := w.Bld[ws.TargetID]
		if b == nil {
			return true, "empty"
		}
		// 建造节奏单独放慢：+4/unit × 25 单位 × 7.2 秒/单位 ≈ 3 分钟（治愈田园，房子要慢慢起）
		b.Progress += 4
		ws.Units++
		ws.NextUnitTick = w.Tick + 72
		if b.Progress >= 100 {
			b.Progress = 100
			b.Complete = true
			return true, "built"
		}
		return false, ""
	}
	return true, ""
}

// DepositAll 把居民背包全部存入领地库存，要求在领主堡旁。
// 每种资源独立限额；装不下的部分**清出背包**（散落/浪费，避免背包永久卡满导致
// "采集一秒就满又交不进去"的死循环），并返回溢出资源清单供上层提示玩家。
func (w *World) DepositAll(a *Actor) (map[string]int, []string, bool) {
	keep := w.Keep()
	if keep == nil || !w.AdjacentToBuilding(a, keep) {
		return nil, nil, false
	}
	if len(a.Carrying) == 0 {
		return map[string]int{}, nil, true
	}
	deposit := map[string]int{}
	var full []string
	for k, v := range a.Carrying {
		put := v
		if space := w.ResCaps[k] - w.Inventory[k]; space < put {
			put = space
			if put < 0 {
				put = 0
			}
			full = append(full, k) // 溢出（含全满）
		}
		if put > 0 {
			w.Inventory[k] += put
			deposit[k] = put
		}
		delete(a.Carrying, k)
	}
	return deposit, full, true
}

// Withdraw 从领地库存取材料。
func (w *World) Withdraw(res string, n int) bool {
	if w.Inventory[res] < n {
		return false
	}
	w.Inventory[res] -= n
	return true
}

// EatMeal 吃一餐：优先面包，否则浆果。返回食物键与是否成功。
func (w *World) EatMeal() (string, bool) {
	if w.Inventory["bread"] > 0 {
		w.Inventory["bread"]--
		return "bread", true
	}
	if w.Inventory["food"] > 0 {
		w.Inventory["food"]--
		return "food", true
	}
	return "", false
}

// Keep 返回领主堡。
func (w *World) Keep() *Building {
	for _, id := range w.BldOrd {
		if w.Bld[id].Kind == BKeep {
			return w.Bld[id]
		}
	}
	return nil
}

// FindFreeHome 给居民分配住处：优先有空位的民居，否则领主堡。
func (w *World) FindFreeHome(owner map[string]string, id string) *Building {
	used := map[string]bool{}
	for _, h := range owner {
		used[h] = true
	}
	for _, bid := range w.BldOrd {
		b := w.Bld[bid]
		if b.Kind == BHouse && b.Complete && !used[b.ID] {
			return b
		}
	}
	return w.Keep()
}

// NearestFreeAround 返回 (x,y) 周围最近的可行瓦片（含自身）。
func (w *World) NearestFreeAround(x, y int) (Pt, bool) {
	if w.Passable(x, y) {
		return Pt{x, y}, true
	}
	for r := 1; r <= 4; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if max(abs(dx), abs(dy)) != r {
					continue
				}
				if w.Passable(x+dx, y+dy) {
					return Pt{x + dx, y + dy}, true
				}
			}
		}
	}
	return Pt{}, false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

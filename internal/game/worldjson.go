package game

import (
	"aitown/internal/sim"
)

// WorldJSON 世界全量数据（一次性下发，前端据此构建静态层）。
type WorldJSON struct {
	// Name 领地名称。
	Name string `json:"name"`
	// Lore 世界传说/背景故事。
	Lore string `json:"lore"`
	// W 地图宽（格）。
	W int `json:"w"`
	// H 地图高（格）。
	H int `json:"h"`
	// Tiles 瓦片数组，行优先 idx = y*W+x；枚举见协议文档"瓦片"。
	Tiles []int `json:"tiles"`
	// Resources 全部资源节点。
	Resources []ResJSON `json:"resources"`
	// Buildings 全部建筑（含工地蓝图）。
	Buildings []BldJSON `json:"buildings"`
	// Agents 全体居民的静态信息与出生点。
	Agents []AgentJSON `json:"agents"`
	// Inventory 领地库存：wood/food/stone。
	Inventory map[string]int `json:"inventory"`
	// Day 当前游戏天数。
	Day int `json:"day"`
	// Hour 当前时刻 0-24。
	Hour float64 `json:"hour"`
}

// ResJSON 资源实体。
type ResJSON struct {
	// ID 资源节点 ID。
	ID string `json:"id"`
	// Kind 资源类型：tree/berry/rock。
	Kind string `json:"kind"`
	// X 所在格横坐标。
	X int `json:"x"`
	// Y 所在格纵坐标。
	Y int `json:"y"`
	// Amount 当前剩余可采数量。
	Amount int `json:"amount"`
	// Max 满值数量（再生上限）。
	Max int `json:"max"`
}

// BldJSON 建筑实体。
type BldJSON struct {
	// ID 建筑 ID。
	ID string `json:"id"`
	// Kind 建筑类型：keep/house/granary/farm。
	Kind string `json:"kind"`
	// Name 显示名（工地为"工地·xxx"）。
	Name string `json:"name"`
	// X 占地左上角横坐标。
	X int `json:"x"`
	// Y 占地左上角纵坐标。
	Y int `json:"y"`
	// W 占地宽（格）。
	W int `json:"w"`
	// H 占地高（格）。
	H int `json:"h"`
	// Progress 施工进度 0-100。
	Progress int `json:"progress"`
	// Complete 是否已落成（false = 工地蓝图，前端渲染半透明）。
	Complete bool `json:"complete"`
}

// AgentJSON 居民静态信息。
type AgentJSON struct {
	// ID 居民 ID。
	ID string `json:"id"`
	// Name 居民名。
	Name string `json:"name"`
	// Role 职业。
	Role string `json:"role"`
	// Tier 阶层：0 平民 / 1 官员。
	Tier int `json:"tier"`
	// Personality 性格描述（LLM 人设来源）。
	Personality string `json:"personality"`
	// Traits 特质标签。
	Traits []string `json:"traits,omitempty"`
	// X 出生点横坐标。
	X float64 `json:"x"`
	// Y 出生点纵坐标。
	Y float64 `json:"y"`
	// HomeID 住所建筑 ID。
	HomeID string `json:"home_id"`
}

func (e *Engine) worldJSONLocked() *WorldJSON {
	w := e.world
	tiles := make([]int, len(w.Tiles))
	for i, t := range w.Tiles {
		tiles[i] = int(t)
	}
	res := make([]ResJSON, 0, len(w.ResOrd))
	for _, id := range w.ResOrd {
		r := w.Res[id]
		res = append(res, ResJSON{ID: r.ID, Kind: string(r.Kind), X: r.X, Y: r.Y, Amount: r.Amount, Max: r.Max})
	}
	blds := make([]BldJSON, 0, len(w.BldOrd))
	for _, id := range w.BldOrd {
		b := w.Bld[id]
		blds = append(blds, BldJSON{ID: b.ID, Kind: string(b.Kind), Name: b.Name,
			X: b.X, Y: b.Y, W: b.W, H: b.H, Progress: b.Progress, Complete: b.Complete})
	}
	agents := make([]AgentJSON, 0, len(e.ord))
	for _, id := range e.ord {
		c := e.cits[id]
		a := w.Actors[id]
		agents = append(agents, AgentJSON{ID: id, Name: c.Name, Role: c.Role, Tier: c.Tier,
			Personality: c.Personality, Traits: c.Traits, X: a.X, Y: a.Y, HomeID: c.HomeID})
	}
	return &WorldJSON{
		Name: w.Name, Lore: w.Lore, W: w.W, H: w.H, Tiles: tiles,
		Resources: res, Buildings: blds, Agents: agents,
		Inventory: map[string]int{
			"wood": w.Inventory["wood"], "food": w.Inventory["food"], "stone": w.Inventory["stone"],
		},
		Day: w.Day(), Hour: w.Hour(),
	}
}

func (e *Engine) snapshotLocked() *SnapshotMsg {
	w := e.world
	agents := make([]AgentView, 0, len(e.ord))
	for _, id := range e.ord {
		a := w.Actors[id]
		state := ""
		action := ""
		// 大脑视图（agent goroutine 原子发布）：talking/sleeping/动作
		if ag := e.agents[id]; ag != nil {
			if v := ag.view.Load(); v != nil {
				state = v.State
				action = v.Action
			}
		}
		if state == "" { // 其余状态由物理身体推导
			switch a.Busy {
			case sim.BusyTalk:
				state = "talking"
			case sim.BusyWork:
				state = "working"
			case sim.BusyMove:
				state = "moving"
			default:
				state = "idle"
			}
		}
		agents = append(agents, AgentView{
			ID: id, X: a.X, Y: a.Y, Facing: a.Facing,
			State: state, Action: action,
			Carrying: a.CarryTotal(),
			Hidden:   a.Hidden,
		})
	}
	res := make([]ResState, 0, len(w.ResOrd))
	for _, id := range w.ResOrd {
		r := w.Res[id]
		res = append(res, ResState{ID: id, Amount: r.Amount, Depleted: r.Depleted})
	}
	blds := make([]BldState, 0, len(w.BldOrd))
	for _, id := range w.BldOrd {
		b := w.Bld[id]
		blds = append(blds, BldState{ID: id, Progress: b.Progress, Complete: b.Complete})
	}
	jobs := make([]JobView, 0, len(e.jobs))
	for _, j := range e.jobs {
		if j.Status == "done" {
			continue
		}
		jobs = append(jobs, JobView{ID: j.ID, Type: j.Type, Title: j.Title,
			Priority: j.Priority, Status: j.Status, ClaimedBy: j.ClaimedBy, Note: j.Note,
			IssuedBy: j.IssuedBy, IssuedByName: e.issuerNameLocked(j.IssuedBy)})
	}
	return &SnapshotMsg{
		T:    "snapshot",
		Tick: w.Tick, Day: w.Day(), Hour: w.Hour(),
		Paused: w.Paused,
		Season: w.Season().Name(),
		Domain: map[string]int{
			"wood":  w.Inventory["wood"],
			"food":  w.Inventory["food"],
			"stone": w.Inventory["stone"],
			"wheat": w.Inventory["wheat"],
			"flour": w.Inventory["flour"],
			"bread": w.Inventory["bread"],
		},
		Agents: agents, Resources: res, Buildings: blds, Jobs: jobs,
		Stats: e.gw.Stats(),
	}
}

// issuerNameLocked 工作单发布者的显示名："lord"→领主、""→领地、居民 ID→姓名（已放逐则回退 ID）。
// 需持锁。
func (e *Engine) issuerNameLocked(id string) string {
	switch id {
	case "":
		return "领地"
	case "lord":
		return "领主"
	}
	if c := e.cits[id]; c != nil {
		return c.Name
	}
	return id
}

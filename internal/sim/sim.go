package sim

// Tile 瓦片地形类型。
type Tile uint8

const (
	Grass Tile = iota // 草地
	Dirt              // 泥土
	Path              // 道路
	Water             // 水（不可通行）
	Sand              // 沙滩
	Farm              // 农田（耕地）
)

// Walkable 是否可通行。
func (t Tile) Walkable() bool { return t != Water }

// ResourceKind 资源节点类型。
type ResourceKind string

const (
	ResTree  ResourceKind = "tree"  // 乔木 → 木材
	ResBerry ResourceKind = "berry" // 浆果丛 → 食物
	ResRock  ResourceKind = "rock"  // 岩石 → 石料
)

// Yield 返回采集产物（领地库存键）。
func (k ResourceKind) Yield() string {
	switch k {
	case ResTree:
		return "wood"
	case ResBerry:
		return "food"
	case ResRock:
		return "stone"
	}
	return ""
}

// Label 中文显示名。
func (k ResourceKind) Label() string {
	switch k {
	case ResTree:
		return "树木"
	case ResBerry:
		return "浆果丛"
	case ResRock:
		return "岩石"
	}
	return string(k)
}

// WorkSeconds 采集一个单位所需的游戏秒数。
func (k ResourceKind) WorkSeconds() float64 {
	switch k {
	case ResTree:
		return 1.2
	case ResBerry:
		return 0.7
	case ResRock:
		return 1.6
	}
	return 1.0
}

// Resource 世界上的资源节点。
type Resource struct {
	ID       string       `json:"id"`
	Kind     ResourceKind `json:"kind"`
	X        int          `json:"x"`
	Y        int          `json:"y"`
	Amount   int          `json:"amount"`
	Max      int          `json:"max"`
	Depleted bool         `json:"depleted"`
	RegrowAt int64        `json:"regrow_at"` // 耗尽后恢复的世界 tick；0 表示未排程
}

// BuildingKind 建筑类型。
type BuildingKind string

const (
	BKeep    BuildingKind = "keep"    // 领主堡：仓库所在，交付点
	BHouse   BuildingKind = "house"   // 民居：居民睡觉
	BGranary BuildingKind = "granary" // 粮仓：提升库存上限
	BFarm    BuildingKind = "farm"    // 农田：可劳作产出小麦
	BMill    BuildingKind = "mill"    // 磨坊：磨面与烘焙
)

// Footprint 返回建筑占地尺寸（格）。
func (k BuildingKind) Footprint() (int, int) {
	switch k {
	case BKeep:
		return 3, 3
	case BHouse:
		return 2, 2
	case BGranary:
		return 2, 2
	case BFarm:
		return 3, 2
	case BMill:
		return 2, 2
	}
	return 1, 1
}

// BuildNeeds 建造所需材料（从领地库存扣除）。
func (k BuildingKind) BuildNeeds() map[string]int {
	switch k {
	case BHouse:
		return map[string]int{"wood": 10}
	case BGranary:
		return map[string]int{"wood": 10, "stone": 6}
	case BFarm:
		return map[string]int{"wood": 6}
	case BMill:
		return map[string]int{"wood": 8, "stone": 4}
	}
	return nil
}

// Label 中文显示名。
func (k BuildingKind) Label() string {
	switch k {
	case BKeep:
		return "领主堡"
	case BHouse:
		return "民居"
	case BGranary:
		return "粮仓"
	case BFarm:
		return "农田"
	case BMill:
		return "磨坊"
	}
	return string(k)
}

// Building 建筑（或工地）。
type Building struct {
	ID       string       `json:"id"`
	Kind     BuildingKind `json:"kind"`
	Name     string       `json:"name"`
	X        int          `json:"x"`
	Y        int          `json:"y"`
	W        int          `json:"w"`
	H        int          `json:"h"`
	Progress int          `json:"progress"` // 0-100，<100 视为工地
	Complete bool         `json:"complete"`
}

// Door 返回默认"门口"瓦片（ footprint 下缘中点，交付/进出参照点）。
func (b *Building) Door() (int, int) {
	return b.X + b.W/2, b.Y + b.H
}

// BusyKind 居民忙碌状态。
type BusyKind uint8

const (
	/*
		iota: 常量计数器，后面自动+1
	*/
	BusyNone BusyKind = iota
	BusyMove
	BusyWork
	BusyTalk
	BusySleep
)

// DaysPerSeason 一个季节的天数；一年 = 4 季 × 7 天 = 28 游戏天。
const DaysPerSeason = 7

// Season 季节：春耕 / 夏育 / 秋收 / 冬歇（见 world.go 的季节修正）。
type Season uint8

const (
	SeasonSpring Season = iota // 春：浆果概率加成（收获季开局）
	SeasonSummer               // 夏：无修正
	SeasonAutumn               // 秋：浆果产量加成（囤粮季）
	SeasonWinter               // 冬：浆果枯竭不再生、农田休耕、移动 ×0.9（天寒地冻）
)

// Name 中文显示名。
func (s Season) Name() string {
	switch s {
	case SeasonSpring:
		return "春"
	case SeasonSummer:
		return "夏"
	case SeasonAutumn:
		return "秋"
	case SeasonWinter:
		return "冬"
	}
	return "?"
}

// WorkKind 劳作类型：采集进背包 / 农田磨坊直接进领地 / 建造推进工地。
const (
	WorkHarvest = "harvest"
	WorkTend    = "tend"
	WorkBuild   = "build"
	WorkGrind   = "grind" // 磨坊：小麦 → 面粉
	WorkBake    = "bake"  // 磨坊：面粉 + 浆果 → 面包
)

// WorkState 进行中的劳作。
type WorkState struct {
	Kind         string
	TargetID     string
	NextUnitTick int64
	Units        int
	MaxUnits     int
}

// Actor 居民的物理存在：位置、朝向、移动/劳作状态、背包。
type Actor struct {
	ID       string
	X, Y     float64 // 以瓦片为单位的连续坐标（tile+0.5 为格心）
	Facing   int     // 0下 1上 2左 3右
	Speed    float64 // 格/秒
	Path     []Pt
	Busy     BusyKind
	Work     *WorkState
	Carrying map[string]int
	// Hidden 村民已进入建筑（前端隐藏精灵）。
	Hidden bool
}

// CarryTotal 背包总量。
func (a *Actor) CarryTotal() int {
	n := 0
	for _, v := range a.Carrying {
		n += v
	}
	return n
}

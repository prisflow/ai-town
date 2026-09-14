package worldgen

import (
	"fmt"
	"strings"
)

// Roles v0 支持的村民职业词表（约束 LLM 输出，映射到模板 roles）。
var Roles = map[string]bool{
	"woodcutter": true, "farmer": true, "builder": true,
	"forager": true, "villager": true,
}

// Villager 村民规格。
type Villager struct {
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Personality string   `json:"personality"`
	Traits      []string `json:"traits,omitempty"`
}

// TraitWords 合法特质标签（约束 LLM 输出）。
var TraitWords = map[string]bool{
	"勤劳": true, "懒散": true, "健谈": true, "腼腆": true,
	"胆大": true, "谨慎": true, "乐观": true, "悲观": true,
	"爱吃": true, "手巧": true,
}

// Spec 世界规格（LLM 产物，可直接 JSON 序列化落盘）。
type Spec struct {
	Name      string     `json:"name"`
	Lore      string     `json:"lore"`
	Villagers []Villager `json:"villagers"`
}

// DefaultSpec 内置默认世界（LLM 不可用时的回退，也与 mock 提供商输出一致）。
func DefaultSpec() *Spec {
	return &Spec{
		Name: "迷雾河谷",
		Lore: "群山之间一条终年起雾的河谷，领主在此扎下领地的第一面旗。土地肥沃，林中猎物与浆果丰饶，但老人们告诫：不要在雾最浓的夜里靠近深水。",
		Villagers: []Villager{
			{Name: "老周", Role: "woodcutter", Personality: "沉默寡言的伐木人，斧头从不离身，话少但句句实在", Traits: []string{"勤劳", "腼腆"}},
			{Name: "杏娘", Role: "farmer", Personality: "泼辣能干的农妇，嗓门大，算账比谁都快", Traits: []string{"勤劳", "乐观"}},
			{Name: "石头", Role: "builder", Personality: "憨厚的年轻石匠，力气大，干活不惜力，有点贪吃", Traits: []string{"爱吃", "胆大"}},
			{Name: "小满", Role: "forager", Personality: "机灵的采药少女，认得河谷里每一丛浆果，爱讲怪谈", Traits: []string{"健谈", "谨慎"}},
			{Name: "陈九", Role: "villager", Personality: "见多识广的老货郎，哪里有路他都知道，爱打听消息", Traits: []string{"健谈", "乐观"}},
			{Name: "阿禾", Role: "farmer", Personality: "细声细气的少年农夫，跟在杏娘身后学种地，胆子小", Traits: []string{"谨慎", "悲观"}},
		},
	}
}

// NormalizeVillager 校验单个村民：非法职业回退 villager、空性格兜底、重名加序号。
func NormalizeVillager(v Villager, taken map[string]bool) Villager {
	if !Roles[v.Role] {
		v.Role = "villager"
	}
	if strings.TrimSpace(v.Personality) == "" {
		v.Personality = "普通的领地居民"
	}
	if strings.TrimSpace(v.Name) == "" {
		v.Name = "新村民"
	}
	if taken[v.Name] {
		for i := 2; ; i++ {
			cand := fmt.Sprintf("%s%d", v.Name, i)
			if !taken[cand] {
				v.Name = cand
				break
			}
		}
	}
	return v
}

// Normalize 校验并修正 LLM 输出：非法职业→villager，人数不足→补默认村民，空名→剔除。
func Normalize(s *Spec) *Spec {
	def := DefaultSpec()
	if strings.TrimSpace(s.Name) == "" {
		s.Name = def.Name
	}
	if strings.TrimSpace(s.Lore) == "" {
		s.Lore = def.Lore
	}
	clean := make([]Villager, 0, len(s.Villagers))
	for _, v := range s.Villagers {
		if strings.TrimSpace(v.Name) == "" {
			continue
		}
		if !Roles[v.Role] {
			v.Role = "villager"
		}
		// 过滤特质标签：只保留合法词
		var traits []string
		for _, t := range v.Traits {
			if TraitWords[t] {
				traits = append(traits, t)
			}
		}
		v.Traits = traits
		if strings.TrimSpace(v.Personality) == "" {
			v.Personality = "普通的领地居民"
		}
		clean = append(clean, v)
	}
	if len(clean) < 4 {
		seen := map[string]bool{}
		for _, v := range clean {
			seen[v.Name] = true
		}
		for _, v := range def.Villagers {
			if len(clean) >= 6 {
				break
			}
			if !seen[v.Name] {
				clean = append(clean, v)
			}
		}
	}
	if len(clean) > 8 {
		clean = clean[:8]
	}
	s.Villagers = clean
	return s
}

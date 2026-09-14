package sim

import (
	"container/heap"
)

// Pt 格点坐标。
/*
	`json:"x"`：元数据/字段标签 把结构体序列化或者反序列化时，字段名应该叫什么
*/
type Pt struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// pathNode A* 搜索节点。
type pathNode struct {
	p      Pt
	g, f   int
	parent int // nodes 下标；-1 为起点
}

// FindPath A* 四方向寻路，返回从起点下一步到终点的路径（不含起点）。
// 终点不可通行时自动改寻终点周围最近的可通行格。找不到返回 (nil,false)。
func (w *World) FindPath(sx, sy, tx, ty int) ([]Pt, bool) {
	if sx == tx && sy == ty {
		return nil, true
	}
	if !w.InBounds(tx, ty) || !w.Passable(tx, ty) {
		t, ok := w.NearestFreeAround(tx, ty)
		if !ok {
			return nil, false
		}
		tx, ty = t.X, t.Y
		if sx == tx && sy == ty {
			return nil, true
		}
	}
	const maxNodes = 8000
	nodes := []pathNode{}
	h := func(p Pt) int { return abs(p.X-tx) + abs(p.Y-ty) }
	open := &nodeHeap{nodes: &nodes}
	best := map[Pt]int{} // pt -> nodes 下标（当前最优）

	start := Pt{sx, sy}
	nodes = append(nodes, pathNode{p: start, g: 0, f: h(start), parent: -1})
	best[start] = 0
	heap.Push(open, 0)
	explored := map[Pt]bool{}

	for open.Len() > 0 && len(nodes) < maxNodes {
		curIdx := heap.Pop(open).(int)
		cur := nodes[curIdx]
		if explored[cur.p] {
			continue
		}
		explored[cur.p] = true
		if cur.p.X == tx && cur.p.Y == ty {
			var rev []Pt
			for i := curIdx; i >= 0; i = nodes[i].parent {
				rev = append(rev, nodes[i].p)
			}
			path := make([]Pt, 0, len(rev)-1)
			for i := len(rev) - 2; i >= 0; i-- {
				path = append(path, rev[i])
			}
			return path, true
		}
		for _, d := range dirs4 {
			nx, ny := cur.p.X+d[0], cur.p.Y+d[1]
			np := Pt{nx, ny}
			if explored[np] || !w.Passable(nx, ny) {
				continue
			}
			ng := cur.g + 1
			if bi, seen := best[np]; seen && nodes[bi].g <= ng {
				continue
			}
			nodes = append(nodes, pathNode{p: np, g: ng, f: ng + h(np), parent: curIdx})
			best[np] = len(nodes) - 1
			heap.Push(open, len(nodes)-1)
		}
	}
	return nil, false
}

var dirs4 = [4][2]int{{0, 1}, {0, -1}, {1, 0}, {-1, 0}}

// nodeHeap 保存 nodes 下标的最小堆，按 f（其次 g）排序。
type nodeHeap struct {
	idx   []int
	nodes *[]pathNode
}

func (h *nodeHeap) Len() int { return len(h.idx) }
func (h *nodeHeap) Less(i, j int) bool {
	a := (*h.nodes)[h.idx[i]]
	b := (*h.nodes)[h.idx[j]]
	if a.f != b.f {
		return a.f < b.f
	}
	return a.g < b.g
}
func (h *nodeHeap) Swap(i, j int) { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }
func (h *nodeHeap) Push(x any)    { h.idx = append(h.idx, x.(int)) }
func (h *nodeHeap) Pop() any {
	old := h.idx
	n := len(old)
	x := old[n-1]
	h.idx = old[:n-1]
	return x
}

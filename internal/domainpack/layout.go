package domainpack

import (
	"math"

	"github.com/kcl1104/geminus/internal/model"
)

// Layout spacing in canvas units for LayeredLayout.
const (
	LayerSpacing = 240.0
	RowSpacing   = 120.0
)

// LayeredLayout returns a position for every node of ir. Existing positions
// are kept; a node without one is placed by its breadth-first distance from
// the sources (x) and the first row of that layer no other node occupies
// (y). Nodes no source reaches go one layer past the last. Iteration follows
// node and flow order in the document, so the layout is deterministic (no
// map order). A duplicated node ID (an invalid IR) is placed once, by its
// first occurrence; flows to unknown nodes are ignored.
func LayeredLayout(ir *model.ModelIR) ViewDefinitions {
	if ir == nil {
		return ViewDefinitions{Positions: map[string]model.Position{}}
	}
	index := make(map[string]int, len(ir.Nodes))
	for i, n := range ir.Nodes {
		if _, dup := index[n.ID]; !dup {
			index[n.ID] = i
		}
	}
	out := make([][]int, len(ir.Nodes))
	for _, f := range ir.Flows {
		from, okF := index[f.From]
		to, okT := index[f.To]
		if okF && okT {
			out[from] = append(out[from], to)
		}
	}
	layer := make([]int, len(ir.Nodes))
	for i := range layer {
		layer[i] = -1
	}
	var queue []int
	for i, n := range ir.Nodes {
		if n.Type == model.NodeSource {
			layer[i] = 0
			queue = append(queue, i)
		}
	}
	deepest := 0
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		for _, j := range out[i] {
			if layer[j] < 0 {
				layer[j] = layer[i] + 1
				deepest = max(deepest, layer[j])
				queue = append(queue, j)
			}
		}
	}
	// A grid cell is taken by any kept position that rounds to it, so a new
	// node never lands on top of an existing one.
	type cell struct{ layer, row int64 }
	cellOf := func(p model.Position) cell {
		return cell{int64(math.Round(p.X / LayerSpacing)), int64(math.Round(p.Y / RowSpacing))}
	}
	positions := make(map[string]model.Position, len(ir.Nodes))
	taken := map[cell]bool{}
	existing := ir.Views.TwoD.Positions
	for i, n := range ir.Nodes {
		if p, ok := existing[n.ID]; ok && index[n.ID] == i {
			positions[n.ID] = p
			if !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0) {
				taken[cellOf(p)] = true
			}
		}
	}
	for i, n := range ir.Nodes {
		if _, placed := positions[n.ID]; placed || index[n.ID] != i {
			continue
		}
		l := layer[i]
		if l < 0 {
			l = deepest + 1
		}
		row := int64(0)
		for taken[cell{int64(l), row}] {
			row++
		}
		taken[cell{int64(l), row}] = true
		positions[n.ID] = model.Position{X: float64(l) * LayerSpacing, Y: float64(row) * RowSpacing}
	}
	return ViewDefinitions{Positions: positions}
}

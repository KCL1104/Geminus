package domainpack

import "github.com/kcl1104/geminus/internal/model"

// Layout spacing in canvas units for LayeredLayout.
const (
	LayerSpacing = 240.0
	RowSpacing   = 120.0
)

// LayeredLayout returns a position for every node of ir. Existing positions
// are kept; a node without one is placed by its breadth-first distance from
// the sources (x) and its order within that layer (y). Nodes no source
// reaches go one layer past the last. Iteration follows node and flow order
// in the document, so the layout is deterministic (no map order).
func LayeredLayout(ir *model.ModelIR) ViewDefinitions {
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
	rows := map[int]int{}
	positions := make(map[string]model.Position, len(ir.Nodes))
	existing := ir.Views.TwoD.Positions
	for i, n := range ir.Nodes {
		if p, ok := existing[n.ID]; ok {
			positions[n.ID] = p
			continue
		}
		l := layer[i]
		if l < 0 {
			l = deepest + 1
		}
		positions[n.ID] = model.Position{X: float64(l) * LayerSpacing, Y: float64(rows[l]) * RowSpacing}
		rows[l]++
	}
	return ViewDefinitions{Positions: positions}
}

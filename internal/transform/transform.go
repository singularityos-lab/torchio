// Package transform is Torchio's format pipeline. The print problem is: a
// document is in format A, the printer accepts {X, Y, Z}, find the cheapest
// sequence of conversions. Torchio models this as a graph whose edges are
// registered Transforms, each with a cost that weighs fidelity as well as CPU
// (PDF passthrough costs nothing; rasterizing costs more because it loses the
// vector structure). The planner runs Dijkstra over that graph. If the printer
// accepts the document's format directly, the plan is empty and the document
// passes verbatim: the fast path never rasterizes.
package transform

import (
	"context"
	"io"
	"math"

	"github.com/singularityos-lab/torchio/internal/model"
)

// Edge describes a transform's place in the format graph.
type Edge struct {
	From model.MIME
	To   model.MIME
	Cost int // must be >= 1 (Dijkstra requires non-negative weights)
}

// Params carries the per-job context a transform may need (the ticket and the
// target printer's capabilities), kept separate from the byte streams.
type Params struct {
	Ticket model.JobTicket
	Caps   model.Capabilities
}

// Transform converts one format into another by streaming bytes.
type Transform interface {
	Edge() Edge
	Run(ctx context.Context, in io.Reader, out io.Writer, p Params) error
}

// Registry holds the known transforms and plans paths through them.
type Registry struct {
	byFrom map[model.MIME][]Transform
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byFrom: make(map[model.MIME][]Transform)}
}

// Register adds a transform (one graph edge) to the registry.
func (r *Registry) Register(t Transform) {
	e := t.Edge()
	r.byFrom[e.From] = append(r.byFrom[e.From], t)
}

// Plan returns the cheapest sequence of transforms taking `from` to any of the
// printer's accepted formats. An empty plan with ok=true means the format is
// already accepted (the fast path). ok=false means no path exists.
func (r *Registry) Plan(from model.MIME, targets []model.MIME) (plan []Transform, ok bool) {
	targetSet := make(map[model.MIME]bool, len(targets))
	for _, t := range targets {
		targetSet[t] = true
	}
	if targetSet[from] {
		return nil, true // already accepted: no transform needed
	}

	dist := map[model.MIME]int{from: 0}
	prev := map[model.MIME]Transform{}
	visited := map[model.MIME]bool{}

	for {
		// Pick the unvisited node with the smallest known distance.
		cur := model.MIME("")
		best := math.MaxInt
		found := false
		for n, d := range dist {
			if !visited[n] && d < best {
				best, cur, found = d, n, true
			}
		}
		if !found {
			return nil, false // exhausted reachable nodes without hitting a target
		}
		if targetSet[cur] {
			return buildPath(prev, cur), true
		}
		visited[cur] = true

		for _, t := range r.byFrom[cur] {
			e := t.Edge()
			nd := dist[cur] + e.Cost
			if old, seen := dist[e.To]; !seen || nd < old {
				dist[e.To] = nd
				prev[e.To] = t
			}
		}
	}
}

// buildPath reconstructs the transform sequence ending at target by walking the
// predecessor links back to the source, then reversing.
func buildPath(prev map[model.MIME]Transform, target model.MIME) []Transform {
	var rev []Transform
	for {
		t, ok := prev[target]
		if !ok {
			break
		}
		rev = append(rev, t)
		target = t.Edge().From
	}
	// Reverse in place.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// FinalFormat returns the format a plan produces (the last hop's target), or
// the input format when the plan is empty.
func FinalFormat(plan []Transform, input model.MIME) model.MIME {
	if len(plan) == 0 {
		return input
	}
	return plan[len(plan)-1].Edge().To
}

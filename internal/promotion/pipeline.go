package promotion

import (
	"fmt"
	"slices"
	"strings"
)

// PipelineOrder is an Order of a pipeline.
type PipelineOrder struct {
	// Product is the Product the Order is a variant of.
	Product string
	// Upstreams names the upstream Orders the Order runs after.
	Upstreams []string
}

// Pipeline holds the Orders of a namespace by name.
type Pipeline map[string]PipelineOrder

// Problem says why the upstream stages of order do not form a valid
// pipeline, "" when they do: an upstream Order that is a variant of another
// Product, or stages that lead back to order. An upstream Order that does
// not exist is no problem of the pipeline: the stage waits for it.
func (p Pipeline) Problem(order string) string {
	self, ok := p[order]
	if !ok {
		return ""
	}
	for _, name := range self.Upstreams {
		if upstream, ok := p[name]; ok && upstream.Product != self.Product {
			return fmt.Sprintf("upstream Order %s is a variant of Product %s, not %s", name, upstream.Product, self.Product)
		}
	}
	if cycle := p.cycle(order); cycle != nil {
		return "the upstream stages form a cycle: " + strings.Join(cycle, " runs after ")
	}
	return ""
}

// cycle returns a path of upstream stages from order back to order, nil
// when there is none.
func (p Pipeline) cycle(order string) []string {
	visited := map[string]bool{}
	path := []string{order}
	var walk func(name string) bool
	walk = func(name string) bool {
		for _, upstream := range p[name].Upstreams {
			if upstream == order {
				path = append(path, upstream)
				return true
			}
			if visited[upstream] {
				continue
			}
			visited[upstream] = true
			path = append(path, upstream)
			if walk(upstream) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	if walk(order) {
		return slices.Clip(path)
	}
	return nil
}

package topology

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/errors"
)

// blastRadius computes the impact of removing or modifying the given resource.
func blastRadius(ctx context.Context, e *Engine, resourceID string) (*ImpactReport, error) {
	graph, err := e.BuildDependencyGraph(ctx)
	if err != nil {
		return nil, err
	}

	target, found := findResource(graph, resourceID)
	if !found {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %s not found in graph", resourceID)
	}

	direct := findDirectDependents(graph, resourceID)
	transitive := findTransitiveDependents(graph, resourceID, direct)
	broken := findBrokenConnections(graph, resourceID)

	return &ImpactReport{
		Target:            target,
		Action:            "delete",
		DirectlyAffected:  direct,
		TransitiveImpact:  transitive,
		BrokenConnections: broken,
		Summary: fmt.Sprintf(
			"deleting %s (%s) affects %d direct and %d transitive resources",
			target.ID, target.Type, len(direct), len(transitive),
		),
	}, nil
}

// dependsOn returns all resources that the given resource directly depends on.
func dependsOn(ctx context.Context, e *Engine, resourceID string) ([]ResourceRef, error) {
	graph, err := e.BuildDependencyGraph(ctx)
	if err != nil {
		return nil, err
	}

	if _, found := findResource(graph, resourceID); !found {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %s not found in graph", resourceID)
	}

	refs := collectDepsFrom(graph, resourceID)

	return refs, nil
}

// collectDepsFrom returns the To side of every dependency whose From matches the ID.
func collectDepsFrom(graph *DependencyGraph, resourceID string) []ResourceRef {
	var refs []ResourceRef

	for i := range graph.Dependencies {
		dep := &graph.Dependencies[i]
		if dep.From.ID == resourceID {
			refs = append(refs, dep.To)
		}
	}

	return refs
}

// dependedBy returns all resources that directly depend on the given resource.
func dependedBy(ctx context.Context, e *Engine, resourceID string) ([]ResourceRef, error) {
	graph, err := e.BuildDependencyGraph(ctx)
	if err != nil {
		return nil, err
	}

	if _, found := findResource(graph, resourceID); !found {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %s not found in graph", resourceID)
	}

	return findDirectDependents(graph, resourceID), nil
}

// findResource locates a resource by ID in the graph.
func findResource(graph *DependencyGraph, resourceID string) (ResourceRef, bool) {
	for _, r := range graph.Resources {
		if r.ID == resourceID {
			return r, true
		}
	}

	return ResourceRef{}, false
}

// findDirectDependents returns resources whose dependencies point TO the given resource.
func findDirectDependents(graph *DependencyGraph, resourceID string) []ResourceRef {
	seen := make(map[string]bool)

	var refs []ResourceRef

	for i := range graph.Dependencies {
		dep := &graph.Dependencies[i]
		if dep.To.ID != resourceID || seen[dep.From.ID] {
			continue
		}

		seen[dep.From.ID] = true

		refs = append(refs, dep.From)
	}

	return refs
}

// findTransitiveDependents walks the graph beyond direct dependents to find
// resources that are transitively affected.
func findTransitiveDependents(
	graph *DependencyGraph,
	rootID string,
	direct []ResourceRef,
) []ResourceRef {
	visited := make(map[string]bool)
	visited[rootID] = true

	for _, d := range direct {
		visited[d.ID] = true
	}

	queue := make([]string, 0, len(direct))
	for _, d := range direct {
		queue = append(queue, d.ID)
	}

	var transitive []ResourceRef

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		queue, transitive = walkDependents(graph, current, visited, queue, transitive)
	}

	return transitive
}

// walkDependents processes one BFS level, appending newly discovered dependents.
func walkDependents(
	graph *DependencyGraph,
	current string,
	visited map[string]bool,
	queue []string,
	transitive []ResourceRef,
) ([]string, []ResourceRef) {
	for i := range graph.Dependencies {
		dep := &graph.Dependencies[i]
		if dep.To.ID != current || visited[dep.From.ID] {
			continue
		}

		visited[dep.From.ID] = true

		transitive = append(transitive, dep.From)
		queue = append(queue, dep.From.ID)
	}

	return queue, transitive
}

// findBrokenConnections returns all dependencies that reference the given resource.
func findBrokenConnections(graph *DependencyGraph, resourceID string) []Dependency {
	var broken []Dependency

	for i := range graph.Dependencies {
		dep := &graph.Dependencies[i]
		if dep.From.ID == resourceID || dep.To.ID == resourceID {
			broken = append(broken, *dep)
		}
	}

	return broken
}

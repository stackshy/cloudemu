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

	allAffected := collectAffectedIDs(resourceID, direct, transitive)
	orphaned := findOrphanedResources(graph, allAffected)

	return &ImpactReport{
		Target:            target,
		Action:            "delete",
		DirectlyAffected:  direct,
		TransitiveImpact:  transitive,
		OrphanedResources: orphaned,
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

// collectAffectedIDs gathers the target + direct + transitive IDs into a set.
func collectAffectedIDs(rootID string, direct, transitive []ResourceRef) map[string]bool {
	affected := map[string]bool{rootID: true}

	for _, r := range direct {
		affected[r.ID] = true
	}

	for _, r := range transitive {
		affected[r.ID] = true
	}

	return affected
}

// findOrphanedResources returns resources outside the affected set whose
// every parent dependency points into the affected set.
func findOrphanedResources(graph *DependencyGraph, affected map[string]bool) []ResourceRef {
	var orphaned []ResourceRef

	for _, r := range graph.Resources {
		if affected[r.ID] {
			continue
		}

		if isOrphaned(graph, r.ID, affected) {
			orphaned = append(orphaned, r)
		}
	}

	return orphaned
}

// isOrphaned checks whether all of a resource's parent dependencies point
// into the affected set (meaning it would lose every parent).
func isOrphaned(graph *DependencyGraph, resourceID string, affected map[string]bool) bool {
	hasParent := false

	for i := range graph.Dependencies {
		dep := &graph.Dependencies[i]
		if dep.From.ID != resourceID {
			continue
		}

		if dep.Type != "member-of" && dep.Type != "attached-to" && dep.Type != "belongs-to" {
			continue
		}

		hasParent = true

		if !affected[dep.To.ID] {
			return false
		}
	}

	return hasParent
}

// whatIf previews the impact of an action on a resource.
func whatIf(ctx context.Context, e *Engine, action, resourceID string) (*ImpactReport, error) {
	switch action {
	case "delete":
		return blastRadius(ctx, e, resourceID)
	case "stop":
		return whatIfStop(ctx, e, resourceID)
	case "disconnect":
		return whatIfDisconnect(ctx, e, resourceID)
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported action: %s", action)
	}
}

// whatIfStop computes the impact of stopping a resource (e.g. an instance).
// Stopping doesn't remove dependencies but breaks connections that require a running state.
func whatIfStop(ctx context.Context, e *Engine, resourceID string) (*ImpactReport, error) {
	graph, err := e.BuildDependencyGraph(ctx)
	if err != nil {
		return nil, err
	}

	target, found := findResource(graph, resourceID)
	if !found {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %s not found in graph", resourceID)
	}

	broken := findBrokenConnections(graph, resourceID)
	direct := findDirectDependents(graph, resourceID)

	return &ImpactReport{
		Target:            target,
		Action:            "stop",
		DirectlyAffected:  direct,
		BrokenConnections: broken,
		Summary: fmt.Sprintf(
			"stopping %s (%s) breaks %d connections",
			target.ID, target.Type, len(broken),
		),
	}, nil
}

// whatIfDisconnect computes the impact of disconnecting a resource
// (e.g. removing a peering connection or detaching an internet gateway).
func whatIfDisconnect(ctx context.Context, e *Engine, resourceID string) (*ImpactReport, error) {
	graph, err := e.BuildDependencyGraph(ctx)
	if err != nil {
		return nil, err
	}

	target, found := findResource(graph, resourceID)
	if !found {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %s not found in graph", resourceID)
	}

	broken := findBrokenConnections(graph, resourceID)
	direct := findDirectDependents(graph, resourceID)
	affected := collectAffectedIDs(resourceID, direct, nil)
	orphaned := findOrphanedResources(graph, affected)

	return &ImpactReport{
		Target:            target,
		Action:            "disconnect",
		DirectlyAffected:  direct,
		OrphanedResources: orphaned,
		BrokenConnections: broken,
		Summary: fmt.Sprintf(
			"disconnecting %s (%s) breaks %d connections, orphans %d resources",
			target.ID, target.Type, len(broken), len(orphaned),
		),
	}, nil
}

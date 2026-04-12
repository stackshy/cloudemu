package topology

import "context"

// ResourceRef identifies a cloud resource in the dependency graph.
type ResourceRef struct {
	ID       string
	Type     string // "vpc", "subnet", "instance", "security-group", etc.
	Name     string
	Provider string // "aws", "azure", "gcp" (empty if unknown)
}

// Dependency represents a directed relationship between two resources.
type Dependency struct {
	From ResourceRef
	To   ResourceRef
	Type string // "member-of", "attached-to", "secured-by", "peers-with"
}

// DependencyGraph holds all discovered resources and their relationships.
type DependencyGraph struct {
	Resources    []ResourceRef
	Dependencies []Dependency
}

// ImpactReport describes the blast radius when a resource is modified or deleted.
type ImpactReport struct {
	Target            ResourceRef
	Action            string
	DirectlyAffected  []ResourceRef
	TransitiveImpact  []ResourceRef
	OrphanedResources []ResourceRef
	BrokenConnections []Dependency
	Summary           string
}

// BuildDependencyGraph scans all compute, networking, and DNS resources
// and returns a graph of their relationships.
func (e *Engine) BuildDependencyGraph(ctx context.Context) (*DependencyGraph, error) {
	return buildGraph(ctx, e)
}

// BlastRadius computes the impact of removing or modifying the given resource.
func (e *Engine) BlastRadius(ctx context.Context, resourceID string) (*ImpactReport, error) {
	return blastRadius(ctx, e, resourceID)
}

// DependsOn returns all resources that the given resource directly depends on.
func (e *Engine) DependsOn(ctx context.Context, resourceID string) ([]ResourceRef, error) {
	return dependsOn(ctx, e, resourceID)
}

// DependedBy returns all resources that directly depend on the given resource.
func (e *Engine) DependedBy(ctx context.Context, resourceID string) ([]ResourceRef, error) {
	return dependedBy(ctx, e, resourceID)
}

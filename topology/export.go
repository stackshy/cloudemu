package topology

import (
	"fmt"
	"strings"
)

// ExportDOT generates a Graphviz DOT representation of the dependency graph.
func (g *DependencyGraph) ExportDOT() string {
	var b strings.Builder

	b.WriteString("digraph CloudEmu {\n")

	for _, r := range g.Resources {
		label := fmt.Sprintf("%s\\n%s", r.Type, r.ID)
		fmt.Fprintf(&b, "  %q [label=%q];\n", r.ID, label)
	}

	for i := range g.Dependencies {
		d := &g.Dependencies[i]
		fmt.Fprintf(&b, "  %q -> %q [label=%q];\n", d.From.ID, d.To.ID, d.Type)
	}

	b.WriteString("}\n")

	return b.String()
}

// ExportMermaid generates a Mermaid diagram representation of the dependency graph.
func (g *DependencyGraph) ExportMermaid() string {
	var b strings.Builder

	b.WriteString("graph TD\n")

	for _, r := range g.Resources {
		label := fmt.Sprintf("%s: %s", r.Type, r.ID)
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", sanitizeMermaidID(r.ID), label)
	}

	for i := range g.Dependencies {
		d := &g.Dependencies[i]
		fmt.Fprintf(
			&b, "  %s -->|%s| %s\n",
			sanitizeMermaidID(d.From.ID),
			d.Type,
			sanitizeMermaidID(d.To.ID),
		)
	}

	return b.String()
}

// sanitizeMermaidID replaces characters that are invalid in Mermaid node IDs.
func sanitizeMermaidID(id string) string {
	replacer := strings.NewReplacer(
		"-", "_",
		"/", "_",
		".", "_",
		":", "_",
	)

	return replacer.Replace(id)
}

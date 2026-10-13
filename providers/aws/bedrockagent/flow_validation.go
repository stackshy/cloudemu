package bedrockagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Flow node types the structural checks key on.
const (
	flowNodeInput  = "Input"
	flowNodeOutput = "Output"
)

// FlowValidationType values the mock reports.
const (
	validationMissingStart   = "MissingStartingNodes"
	validationMissingEnd     = "MissingEndingNodes"
	validationUnknownSource  = "UnknownConnectionSource"
	validationUnknownTarget  = "UnknownConnectionTarget"
	validationDuplicateConns = "DuplicateConnections"
	validationUnreachable    = "UnreachableNode"
)

// flowDefinition is the part of a FlowDefinition the checks read.
type flowDefinition struct {
	Nodes []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"nodes"`
	Connections []struct {
		Name          string          `json:"name"`
		Source        string          `json:"source"`
		Target        string          `json:"target"`
		Configuration json.RawMessage `json:"configuration"`
	} `json:"connections"`
}

// ValidateFlowDefinition runs the structural checks of a flow definition: an
// Input and an Output node exist, every connection joins known nodes, no
// connection is duplicated, and every node is reachable from an Input node.
// It does not check node configurations or data types.
func (*Mock) ValidateFlowDefinition(_ context.Context, definition json.RawMessage) ([]driver.FlowValidation, error) {
	var v violations

	v.required("definition", isNull(definition))

	if err := v.err(); err != nil {
		return nil, err
	}

	var def flowDefinition
	if err := json.Unmarshal(definition, &def); err != nil {
		return nil, errors.New(errors.InvalidArgument, "definition is not a valid object")
	}

	var c flowChecks

	nodes, starts := c.checkNodes(&def)
	edges := c.checkConnections(&def, nodes)

	if len(starts) > 0 {
		reached := reachable(starts, edges)

		for _, n := range def.Nodes {
			if !reached[n.Name] {
				c.add(validationUnreachable, "Node %s can't be reached from an Input node.", n.Name)
			}
		}
	}

	if c.found == nil {
		return []driver.FlowValidation{}, nil
	}

	return c.found, nil
}

// flowChecks collects the problems found in a definition.
type flowChecks struct {
	found []driver.FlowValidation
}

func (c *flowChecks) add(typ, format string, args ...any) {
	c.found = append(c.found, driver.FlowValidation{
		Message: fmt.Sprintf(format, args...), Severity: driver.FlowValidationError, Type: typ,
	})
}

// checkNodes reports a missing Input or Output node and returns the node
// names and the Input nodes.
func (c *flowChecks) checkNodes(def *flowDefinition) (nodes map[string]bool, starts []string) {
	nodes = map[string]bool{}
	hasEnd := false

	for _, n := range def.Nodes {
		nodes[n.Name] = true

		switch n.Type {
		case flowNodeInput:
			starts = append(starts, n.Name)
		case flowNodeOutput:
			hasEnd = true
		}
	}

	if len(starts) == 0 {
		c.add(validationMissingStart, "The flow has no Input node.")
	}

	if !hasEnd {
		c.add(validationMissingEnd, "The flow has no Output node.")
	}

	return nodes, starts
}

// checkConnections reports connections to unknown nodes and duplicates, and
// returns the edges.
func (c *flowChecks) checkConnections(def *flowDefinition, nodes map[string]bool) map[string][]string {
	edges, seen := map[string][]string{}, map[string]bool{}

	for _, conn := range def.Connections {
		if !nodes[conn.Source] {
			c.add(validationUnknownSource, "Connection %s has an unknown source node %s.", conn.Name, conn.Source)
		}

		if !nodes[conn.Target] {
			c.add(validationUnknownTarget, "Connection %s has an unknown target node %s.", conn.Name, conn.Target)
		}

		key := conn.Source + "\x00" + conn.Target + "\x00" + string(conn.Configuration)
		if seen[key] {
			c.add(validationDuplicateConns, "Connection %s duplicates another connection from %s to %s.",
				conn.Name, conn.Source, conn.Target)
		}

		seen[key] = true

		edges[conn.Source] = append(edges[conn.Source], conn.Target)
	}

	return edges
}

// reachable returns every node reachable from starts along edges.
func reachable(starts []string, edges map[string][]string) map[string]bool {
	seen := map[string]bool{}

	queue := append([]string(nil), starts...)

	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]

		if seen[n] {
			continue
		}

		seen[n] = true

		queue = append(queue, edges[n]...)
	}

	return seen
}

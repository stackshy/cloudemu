package memorydb

// nodeTypeFamilies returns the MemoryDB node-type families, each ordered from the
// smallest to the largest node in the family. Scaling in MemoryDB moves a cluster
// to a different node within the same family, so the allowed scale-up / scale-down
// targets for a given node type are the entries above / below it in its family.
//
// Modeling scaling as within-family is a deliberate simplification: real MemoryDB
// also permits some cross-family moves, but the family ladder captures the
// behavior a client depends on — the current type is never offered back, larger
// types appear only under scale-up, smaller only under scale-down, and the ends of
// each ladder (the smallest / largest node) correctly offer nothing in one
// direction.
func nodeTypeFamilies() [][]string {
	return [][]string{
		{"db.t4g.small", "db.t4g.medium"},
		{
			"db.r6g.large", "db.r6g.xlarge", "db.r6g.2xlarge", "db.r6g.4xlarge",
			"db.r6g.8xlarge", "db.r6g.12xlarge", "db.r6g.16xlarge",
		},
		{"db.r6gd.xlarge", "db.r6gd.2xlarge", "db.r6gd.4xlarge", "db.r6gd.8xlarge"},
		{
			"db.r7g.large", "db.r7g.xlarge", "db.r7g.2xlarge", "db.r7g.4xlarge",
			"db.r7g.8xlarge", "db.r7g.12xlarge", "db.r7g.16xlarge",
		},
	}
}

// allowedNodeTypeUpdates returns the node types a cluster of the given current
// node type may scale up to (larger nodes in its family) and scale down to
// (smaller nodes in its family). A node type outside the known families yields
// empty lists rather than an error, matching the API's tolerance of custom types.
// Both results are non-nil so they serialize as JSON arrays.
func allowedNodeTypeUpdates(current string) (scaleUp, scaleDown []string) {
	scaleUp, scaleDown = []string{}, []string{}

	for _, fam := range nodeTypeFamilies() {
		for i, nt := range fam {
			if nt != current {
				continue
			}

			scaleUp = append(scaleUp, fam[i+1:]...)
			scaleDown = append(scaleDown, fam[:i]...)

			return scaleUp, scaleDown
		}
	}

	return scaleUp, scaleDown
}

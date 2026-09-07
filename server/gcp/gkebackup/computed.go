package gkebackup

import "encoding/json"

// seedBackupPlanCounts injects a backupPlan's output-only protectedPodCount and
// protectedNamespaceCount. The mock has no backup data plane, so no workloads
// are protected yet; the counts are a stable 0, which a Terraform computed
// attribute reads back without drifting. A caller cannot pin them — they are
// stripped from the request body (see outputKeys) — so this always sets 0.
func seedBackupPlanCounts(m map[string]json.RawMessage) {
	m["protectedPodCount"] = json.RawMessage("0")
	m["protectedNamespaceCount"] = json.RawMessage("0")
}

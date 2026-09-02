package synapse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// In-memory control-plane model for Azure Synapse. Workspaces and their child
// SQL pools, Spark (big data) pools and integration runtimes are Azure-only ARM
// resources with no cross-cloud portable-driver equivalent, so their state
// lives here on the handler, exactly like the Event Hubs control-plane handler.

const (
	// provisioningSucceeded is the terminal provisioning state every resource
	// reports, so an armsynapse Begin* poller finalizes on its first poll.
	provisioningSucceeded = "Succeeded"

	// SQL pool run states.
	sqlPoolStatusOnline = "Online"
	sqlPoolStatusPaused = "Paused"

	// Integration runtime run states reported by start/stop.
	irStateStarted = "Started"
	irStateStopped = "Stopped"

	// defaultIRType is the integration-runtime type discriminator used when a
	// stored runtime's properties do not name one.
	defaultIRType = "Managed"

	// uidHashLen is how many hex chars of a workspace id hash seed its stable
	// synthetic workspaceUID.
	uidHashLen = 32

	// dnsSuffix is the Synapse dev/web endpoint DNS suffix.
	dnsSuffix = ".dev.azuresynapse.net"
	sqlSuffix = ".sql.azuresynapse.net"
)

// workspaceState is a stored workspace and its child resources.
type workspaceState struct {
	Subscription  string
	ResourceGroup string
	Name          string
	Location      string
	Tags          map[string]string
	Identity      json.RawMessage
	Props         workspaceReqProps
	WorkspaceUID  string

	SQLPools     map[string]*sqlPoolState
	BigDataPools map[string]*bigDataPoolState
	IntRuntimes  map[string]*intRuntimeState
}

type sqlPoolState struct {
	Name     string
	Location string
	Tags     map[string]string
	SKU      *sku
	Props    sqlPoolReqProps
	Status   string
}

type bigDataPoolState struct {
	Name     string
	Location string
	Tags     map[string]string
	Props    bigDataPoolReqProps
}

type intRuntimeState struct {
	Name  string
	Props json.RawMessage
}

func newWorkspaceState(sub, rg, name string) *workspaceState {
	ws := &workspaceState{
		Subscription:  sub,
		ResourceGroup: rg,
		Name:          name,
		SQLPools:      map[string]*sqlPoolState{},
		BigDataPools:  map[string]*bigDataPoolState{},
		IntRuntimes:   map[string]*intRuntimeState{},
	}
	ws.WorkspaceUID = workspaceUID(ws.Subscription + "/" + rg + "/" + name)

	return ws
}

// connectivityEndpoints returns the synthetic dev/web/sql endpoints real Azure
// mints for a workspace, keyed by the same map keys the SDK reads.
func (ws *workspaceState) connectivityEndpoints() map[string]string {
	host := strings.ToLower(ws.Name)

	return map[string]string{
		"web":         "https://web.azuresynapse.net?workspace=" + host,
		"dev":         "https://" + host + dnsSuffix,
		"sqlOnDemand": host + "-ondemand" + sqlSuffix,
		"sql":         host + sqlSuffix,
	}
}

// workspaceUID derives a stable GUID-shaped identifier from the workspace's
// fully qualified name, so its workspaceUID is deterministic across reads.
func workspaceUID(seed string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(seed)))
	h := hex.EncodeToString(sum[:])[:uidHashLen]

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// irType extracts the polymorphic type discriminator from stored integration
// runtime properties, falling back to Managed when absent or unparsable.
func irType(props json.RawMessage) string {
	var probe struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(props, &probe); err == nil && probe.Type != "" {
		return probe.Type
	}

	return defaultIRType
}

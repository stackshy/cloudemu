package kusto

import "time"

// In-memory control-plane model for Kusto (Azure Data Explorer). Clusters and
// their databases are Azure-only ARM containers with no cross-cloud portable-
// driver equivalent, so their state lives here on the handler, scoped to the
// parent cluster. The query data plane keeps its own table store on the
// DataPlaneHandler (see tablestore.go), independent of this ARM state.

const (
	// stateRunning / stateStopped are the cluster lifecycle states real Kusto
	// reports; Start moves a cluster to Running and Stop to Stopped.
	stateRunning = "Running"
	stateStopped = "Stopped"

	// provisioningSucceeded is the terminal provisioningState every synchronous
	// LRO response carries so the SDK poller finalizes on the first poll.
	provisioningSucceeded = "Succeeded"

	// kindReadWrite is the only database kind this control plane serves.
	kindReadWrite = "ReadWrite"

	// defaultLocation is used when a create request omits location.
	defaultLocation = "eastus"

	// kustoHost is the DNS suffix a Kusto cluster's query and ingestion URIs use.
	kustoHost = ".kusto.windows.net"
	// ingestPrefix is prepended to the cluster host for the data-ingestion URI.
	ingestPrefix = "ingest-"
)

type clusterState struct {
	Name          string
	Location      string
	Subscription  string
	ResourceGroup string
	Tags          map[string]string
	Zones         []string
	SKU           kustoSKU
	Properties    clusterProperties
	State         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Databases     map[string]*databaseState
}

type databaseState struct {
	Name       string
	Location   string
	Properties databaseProperties
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

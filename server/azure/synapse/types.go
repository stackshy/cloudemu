package synapse

import "encoding/json"

// ARM resource type strings reported in response bodies.
const (
	armTypeWorkspace   = providerName + "/workspaces"
	armTypeSQLPool     = providerName + "/workspaces/sqlPools"
	armTypeBigDataPool = providerName + "/workspaces/bigDataPools"
	armTypeIntRuntime  = providerName + "/workspaces/integrationRuntimes"
)

// listEnvelope is the ARM {value:[...]} collection response. nextLink is omitted:
// the emulator returns a single page.
type listEnvelope struct {
	Value    []any  `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// --- Workspaces ---

// workspaceRequest is the ARM PUT/PATCH body for a workspace.
type workspaceRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Identity   json.RawMessage   `json:"identity,omitempty"`
	Properties workspaceReqProps `json:"properties"`
}

type workspaceReqProps struct {
	DefaultDataLakeStorage        *dataLakeStorage `json:"defaultDataLakeStorage,omitempty"`
	SQLAdministratorLogin         string           `json:"sqlAdministratorLogin,omitempty"`
	SQLAdministratorLoginPassword string           `json:"sqlAdministratorLoginPassword,omitempty"`
	ManagedResourceGroupName      string           `json:"managedResourceGroupName,omitempty"`
	ManagedVirtualNetwork         string           `json:"managedVirtualNetwork,omitempty"`
	PublicNetworkAccess           string           `json:"publicNetworkAccess,omitempty"`
}

type dataLakeStorage struct {
	AccountURL                   string `json:"accountUrl,omitempty"`
	Filesystem                   string `json:"filesystem,omitempty"`
	ResourceID                   string `json:"resourceId,omitempty"`
	CreateManagedPrivateEndpoint *bool  `json:"createManagedPrivateEndpoint,omitempty"`
}

type workspaceResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Identity   json.RawMessage    `json:"identity,omitempty"`
	Properties workspaceRespProps `json:"properties"`
}

type workspaceRespProps struct {
	ProvisioningState        string            `json:"provisioningState"`
	DefaultDataLakeStorage   *dataLakeStorage  `json:"defaultDataLakeStorage,omitempty"`
	SQLAdministratorLogin    string            `json:"sqlAdministratorLogin,omitempty"`
	ManagedResourceGroupName string            `json:"managedResourceGroupName,omitempty"`
	ManagedVirtualNetwork    string            `json:"managedVirtualNetwork,omitempty"`
	PublicNetworkAccess      string            `json:"publicNetworkAccess,omitempty"`
	ConnectivityEndpoints    map[string]string `json:"connectivityEndpoints,omitempty"`
	WorkspaceUID             string            `json:"workspaceUID,omitempty"`
}

// --- SQL pools ---

type sku struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity *int32 `json:"capacity,omitempty"`
}

type sqlPoolRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *sku              `json:"sku,omitempty"`
	Properties sqlPoolReqProps   `json:"properties"`
}

type sqlPoolReqProps struct {
	Collation          string `json:"collation,omitempty"`
	MaxSizeBytes       *int64 `json:"maxSizeBytes,omitempty"`
	CreateMode         string `json:"createMode,omitempty"`
	StorageAccountType string `json:"storageAccountType,omitempty"`
}

type sqlPoolResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *sku              `json:"sku,omitempty"`
	Properties sqlPoolRespProps  `json:"properties"`
}

type sqlPoolRespProps struct {
	ProvisioningState  string `json:"provisioningState"`
	Status             string `json:"status"`
	Collation          string `json:"collation,omitempty"`
	MaxSizeBytes       *int64 `json:"maxSizeBytes,omitempty"`
	CreateMode         string `json:"createMode,omitempty"`
	StorageAccountType string `json:"storageAccountType,omitempty"`
}

// --- Big Data (Spark) pools ---

type autoScale struct {
	Enabled      *bool  `json:"enabled,omitempty"`
	MinNodeCount *int32 `json:"minNodeCount,omitempty"`
	MaxNodeCount *int32 `json:"maxNodeCount,omitempty"`
}

type autoPause struct {
	Enabled        *bool  `json:"enabled,omitempty"`
	DelayInMinutes *int32 `json:"delayInMinutes,omitempty"`
}

type bigDataPoolRequest struct {
	Location   string              `json:"location"`
	Tags       map[string]string   `json:"tags,omitempty"`
	Properties bigDataPoolReqProps `json:"properties"`
}

type bigDataPoolReqProps struct {
	NodeCount      *int32     `json:"nodeCount,omitempty"`
	NodeSize       string     `json:"nodeSize,omitempty"`
	NodeSizeFamily string     `json:"nodeSizeFamily,omitempty"`
	AutoScale      *autoScale `json:"autoScale,omitempty"`
	AutoPause      *autoPause `json:"autoPause,omitempty"`
	SparkVersion   string     `json:"sparkVersion,omitempty"`
}

type bigDataPoolResponse struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Location   string               `json:"location"`
	Tags       map[string]string    `json:"tags,omitempty"`
	Properties bigDataPoolRespProps `json:"properties"`
}

type bigDataPoolRespProps struct {
	ProvisioningState string     `json:"provisioningState"`
	NodeCount         *int32     `json:"nodeCount,omitempty"`
	NodeSize          string     `json:"nodeSize,omitempty"`
	NodeSizeFamily    string     `json:"nodeSizeFamily,omitempty"`
	AutoScale         *autoScale `json:"autoScale,omitempty"`
	AutoPause         *autoPause `json:"autoPause,omitempty"`
	SparkVersion      string     `json:"sparkVersion,omitempty"`
}

// --- Integration runtimes ---
//
// Integration runtime properties are a polymorphic ARM union (Managed vs
// SelfHosted, discriminated by properties.type). The handler stores and echoes
// them verbatim as raw JSON so the shape round-trips faithfully without the
// emulator modeling every variant.

type integrationRuntimeRequest struct {
	Properties json.RawMessage `json:"properties"`
}

type integrationRuntimeResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Etag       string          `json:"etag,omitempty"`
	Properties json.RawMessage `json:"properties"`
}

// integrationRuntimeStatusResponse is the ARM shape returned by start/stop. Its
// properties carry the runtime type discriminator and the resulting state.
type integrationRuntimeStatusResponse struct {
	Name       string                        `json:"name"`
	Properties integrationRuntimeStatusProps `json:"properties"`
}

type integrationRuntimeStatusProps struct {
	Type            string `json:"type"`
	State           string `json:"state"`
	DataFactoryName string `json:"dataFactoryName,omitempty"`
}

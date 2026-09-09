package batch

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/batch"
)

// accountRequest is the ARM batch account PUT/PATCH body. location and tags are
// top-level; the rest live under properties.
type accountRequest struct {
	Location   string                    `json:"location"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Properties *accountPropertiesRequest `json:"properties,omitempty"`
}

// accountPropertiesRequest is the writable subset of account properties.
type accountPropertiesRequest struct {
	PoolAllocationMode *string          `json:"poolAllocationMode,omitempty"`
	AutoStorage        *autoStorageWire `json:"autoStorage,omitempty"`
}

// autoStorageWire is the auto-storage block; only the storage account id is
// writable.
type autoStorageWire struct {
	StorageAccountID string `json:"storageAccountId"`
}

// accountResponse is the ARM representation of a batch account.
type accountResponse struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Type       string                    `json:"type"`
	Location   string                    `json:"location"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Properties accountPropertiesResponse `json:"properties"`
}

// accountPropertiesResponse is the account properties block. The computed fields
// (endpoints, provisioningState, quotas) are stable across reads; the account
// keys are deliberately absent — they surface only via listKeys.
type accountPropertiesResponse struct {
	AccountEndpoint                       string           `json:"accountEndpoint"`
	NodeManagementEndpoint                string           `json:"nodeManagementEndpoint"`
	ProvisioningState                     string           `json:"provisioningState"`
	PoolAllocationMode                    string           `json:"poolAllocationMode"`
	DedicatedCoreQuota                    int              `json:"dedicatedCoreQuota"`
	LowPriorityCoreQuota                  int              `json:"lowPriorityCoreQuota"`
	DedicatedCoreQuotaPerVMFamilyEnforced bool             `json:"dedicatedCoreQuotaPerVMFamilyEnforced"`
	PoolQuota                             int              `json:"poolQuota"`
	ActiveJobAndJobScheduleQuota          int              `json:"activeJobAndJobScheduleQuota"`
	AutoStorage                           *autoStorageWire `json:"autoStorage,omitempty"`
}

// accountKeysResponse is the ARM listKeys/regenerateKey action body.
type accountKeysResponse struct {
	AccountName string `json:"accountName"`
	Primary     string `json:"primary"`
	Secondary   string `json:"secondary"`
}

// regenerateKeyRequest is the regenerateKey POST body: which key to rotate.
type regenerateKeyRequest struct {
	KeyName string `json:"keyName"`
}

// accountListResponse is the ARM account list envelope. nextLink is omitted — the
// emulator returns a single page.
type accountListResponse struct {
	Value []accountResponse `json:"value"`
}

// poolRequest is the ARM pool PUT/PATCH body. tags are top-level; the rest live
// under properties.
type poolRequest struct {
	Tags       map[string]string      `json:"tags,omitempty"`
	Properties *poolPropertiesRequest `json:"properties,omitempty"`
}

// poolPropertiesRequest is the writable subset of pool properties.
type poolPropertiesRequest struct {
	VMSize                  string          `json:"vmSize,omitempty"`
	DisplayName             *string         `json:"displayName,omitempty"`
	DeploymentConfiguration json.RawMessage `json:"deploymentConfiguration,omitempty"`
	ScaleSettings           *scaleSettings  `json:"scaleSettings,omitempty"`
}

// scaleSettings carries the fixed-scale node targets. Autoscale is out of scope.
type scaleSettings struct {
	FixedScale *fixedScale `json:"fixedScale,omitempty"`
}

// fixedScale is the fixed-scale target node counts and resize timeout.
type fixedScale struct {
	TargetDedicatedNodes   *int   `json:"targetDedicatedNodes,omitempty"`
	TargetLowPriorityNodes *int   `json:"targetLowPriorityNodes,omitempty"`
	ResizeTimeout          string `json:"resizeTimeout,omitempty"`
}

// resizeRequest is the resize POST body: the desired node targets.
type resizeRequest struct {
	Properties *resizeProperties `json:"properties,omitempty"`
	// The flat form is also accepted so a caller may POST targets directly.
	TargetDedicatedNodes   *int `json:"targetDedicatedNodes,omitempty"`
	TargetLowPriorityNodes *int `json:"targetLowPriorityNodes,omitempty"`
}

// resizeProperties carries the resize targets under a properties envelope.
type resizeProperties struct {
	TargetDedicatedNodes   *int `json:"targetDedicatedNodes,omitempty"`
	TargetLowPriorityNodes *int `json:"targetLowPriorityNodes,omitempty"`
}

// poolResponse is the ARM representation of a batch pool. Its name is
// "<account>/<pool>", matching real Azure.
type poolResponse struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Tags       map[string]string      `json:"tags,omitempty"`
	Properties poolPropertiesResponse `json:"properties"`
}

// poolPropertiesResponse is the pool properties block. The computed fields
// (allocationState, provisioningState, current node counts) are stable.
type poolPropertiesResponse struct {
	VMSize                  string          `json:"vmSize,omitempty"`
	DisplayName             string          `json:"displayName,omitempty"`
	AllocationState         string          `json:"allocationState"`
	ProvisioningState       string          `json:"provisioningState"`
	CurrentDedicatedNodes   int             `json:"currentDedicatedNodes"`
	CurrentLowPriorityNodes int             `json:"currentLowPriorityNodes"`
	ScaleSettings           *scaleSettings  `json:"scaleSettings,omitempty"`
	DeploymentConfiguration json.RawMessage `json:"deploymentConfiguration,omitempty"`
}

// poolListResponse is the ARM pool list envelope.
type poolListResponse struct {
	Value []poolResponse `json:"value"`
}

// toAccountResponse projects a stored account onto its ARM wire representation.
// The keys are intentionally not projected.
func toAccountResponse(a *batch.Account) accountResponse {
	out := accountResponse{
		ID:       a.ARMID(),
		Name:     a.Name,
		Type:     accountArmType,
		Location: a.Location,
		Tags:     a.Tags,
		Properties: accountPropertiesResponse{
			AccountEndpoint:                       a.AccountEndpoint,
			NodeManagementEndpoint:                a.NodeManagementEndpoint,
			ProvisioningState:                     a.ProvisioningState,
			PoolAllocationMode:                    a.PoolAllocationMode,
			DedicatedCoreQuota:                    a.DedicatedCoreQuota,
			LowPriorityCoreQuota:                  a.LowPriorityCoreQuota,
			DedicatedCoreQuotaPerVMFamilyEnforced: a.DedicatedCoreQuotaPerVMFamilyEnforced,
			PoolQuota:                             a.PoolQuota,
			ActiveJobAndJobScheduleQuota:          a.ActiveJobAndJobScheduleQuota,
		},
	}

	if a.AutoStorageAccountID != "" {
		out.Properties.AutoStorage = &autoStorageWire{StorageAccountID: a.AutoStorageAccountID}
	}

	return out
}

// toAccountKeysResponse projects an account's keys onto the listKeys body.
func toAccountKeysResponse(k *batch.AccountKeys) accountKeysResponse {
	return accountKeysResponse{AccountName: k.AccountName, Primary: k.Primary, Secondary: k.Secondary}
}

// toPoolResponse projects a stored pool onto its ARM wire representation.
func toPoolResponse(p *batch.Pool) poolResponse {
	out := poolResponse{
		ID:   p.ARMID(),
		Name: p.AccountName + "/" + p.Name,
		Type: poolArmType,
		Tags: p.Tags,
		Properties: poolPropertiesResponse{
			VMSize:                  p.VMSize,
			DisplayName:             p.DisplayName,
			AllocationState:         p.AllocationState,
			ProvisioningState:       p.ProvisioningState,
			CurrentDedicatedNodes:   p.CurrentDedicatedNodes,
			CurrentLowPriorityNodes: p.CurrentLowPriorityNodes,
			DeploymentConfiguration: p.DeploymentConfiguration,
			ScaleSettings: &scaleSettings{FixedScale: &fixedScale{
				TargetDedicatedNodes:   intPtr(p.TargetDedicatedNodes),
				TargetLowPriorityNodes: intPtr(p.TargetLowPriorityNodes),
				ResizeTimeout:          p.ResizeTimeout,
			}},
		},
	}

	return out
}

// accountInputFromRequest builds an account create/update Input from a request
// body. Pointer fields are carried through so an absent field falls back to the
// stored (or default) value in the driver, which makes a PATCH body merge on its
// own.
func accountInputFromRequest(req *accountRequest) batch.AccountInput {
	in := batch.AccountInput{Tags: req.Tags}

	if req.Properties == nil {
		return in
	}

	in.PoolAllocationMode = req.Properties.PoolAllocationMode

	if req.Properties.AutoStorage != nil {
		id := req.Properties.AutoStorage.StorageAccountID
		in.AutoStorageAccountID = &id
	}

	return in
}

// poolInputFromRequest builds a pool create/update Input from a request body.
func poolInputFromRequest(req *poolRequest) batch.PoolInput {
	in := batch.PoolInput{Tags: req.Tags}

	if req.Properties == nil {
		return in
	}

	p := req.Properties
	if p.VMSize != "" {
		in.VMSize = &p.VMSize
	}

	in.DisplayName = p.DisplayName
	in.DeploymentConfiguration = p.DeploymentConfiguration

	if p.ScaleSettings != nil && p.ScaleSettings.FixedScale != nil {
		fs := p.ScaleSettings.FixedScale
		in.TargetDedicatedNodes = fs.TargetDedicatedNodes
		in.TargetLowPriorityNodes = fs.TargetLowPriorityNodes

		if fs.ResizeTimeout != "" {
			rt := fs.ResizeTimeout
			in.ResizeTimeout = &rt
		}
	}

	return in
}

// resizeTargets extracts the dedicated/low-priority targets from a resize body,
// accepting both the properties-wrapped and the flat forms.
func resizeTargets(req *resizeRequest) (dedicated, lowPriority *int) {
	if req.Properties != nil {
		return req.Properties.TargetDedicatedNodes, req.Properties.TargetLowPriorityNodes
	}

	return req.TargetDedicatedNodes, req.TargetLowPriorityNodes
}

// intPtr returns a pointer to a copy of n, so response bodies never alias a
// stored field.
func intPtr(n int) *int {
	v := n

	return &v
}

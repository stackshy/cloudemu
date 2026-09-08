// Package recoveryservices serves the Azure Recovery Services ARM API
// (Microsoft.RecoveryServices/vaults plus the nested backupPolicies collection
// and the singleton backupconfig / backupstorageconfig resources). Real
// armrecoveryservices VaultsClient and armrecoveryservicesbackup
// ProtectionPoliciesClient / BackupResourceVaultConfigsClient /
// BackupResourceStorageConfigsNonCRRClient requests hit this handler the same way
// they hit management.azure.com.
//
// Real Azure runs vault create and delete as long-running operations; the
// emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded. The Site Recovery replication surface (fabrics,
// protection containers, recovery plans), protected items, recovery points and
// backup-job execution are the data plane and are out of scope.
package recoveryservices

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/providers/azure/recoveryservices"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.RecoveryServices"
	vaultType    = "vaults"
	vaultArmType = providerName + "/" + vaultType

	configArmType        = vaultArmType + "/backupconfig"
	storageConfigArmType = vaultArmType + "/backupstorageconfig"

	vaultConfigName        = "vaultconfig"
	vaultStorageConfigName = "vaultstorageconfig"
)

// Store is the minimal Recovery Services backend the handler needs.
// *recoveryservices.Mock satisfies it.
type Store interface {
	CreateOrUpdateVault(
		ctx context.Context, sub, rg, name, location string, in *recoveryservices.VaultInput,
	) (recoveryservices.Vault, bool, error)
	UpdateVault(ctx context.Context, sub, rg, name string, in *recoveryservices.VaultInput) (recoveryservices.Vault, error)
	GetVault(ctx context.Context, sub, rg, name string) (recoveryservices.Vault, error)
	DeleteVault(ctx context.Context, sub, rg, name string) (bool, error)
	ListVaultsByResourceGroup(ctx context.Context, sub, rg string) ([]recoveryservices.Vault, error)
	ListVaultsBySubscription(ctx context.Context, sub string) ([]recoveryservices.Vault, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error

	GetVaultConfig(ctx context.Context, sub, rg, vault string) (recoveryservices.Config, error)
	UpdateVaultConfig(ctx context.Context, sub, rg, vault string, props json.RawMessage) (recoveryservices.Config, error)
	GetStorageConfig(ctx context.Context, sub, rg, vault string) (recoveryservices.Config, error)
	UpdateStorageConfig(ctx context.Context, sub, rg, vault string, props json.RawMessage) (recoveryservices.Config, error)

	CreateOrUpdatePolicy(
		ctx context.Context, sub, rg, vault, name string, props json.RawMessage,
	) (recoveryservices.BackupPolicy, bool, error)
	GetPolicy(ctx context.Context, sub, rg, vault, name string) (recoveryservices.BackupPolicy, error)
	DeletePolicy(ctx context.Context, sub, rg, vault, name string) (bool, error)
	ListPolicies(ctx context.Context, sub, rg, vault string) ([]recoveryservices.BackupPolicy, error)
}

// Handler serves Microsoft.RecoveryServices/vaults (and nested child resources)
// ARM requests.
type Handler struct {
	store Store
}

// New returns a Recovery Services handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a Recovery Services ARM URL. The provider and
// type are matched case-insensitively.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, vaultType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.listVaults(w, r, &rp)
		return
	}

	if rp.SubResource == "" {
		h.serveVault(w, r, &rp)
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case recoveryservices.PolicySegment:
		h.servePolicy(w, r, &rp)
	case recoveryservices.ConfigSegment:
		h.serveVaultConfig(w, r, &rp)
	case recoveryservices.StorageConfigSegment:
		h.serveStorageConfig(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown sub-resource "+rp.SubResource)
	}
}

// PurgeResourceGroup deletes every vault and child under sub/rg so a
// resource-group delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveVault routes the top-level vault CRUD surface.
func (h *Handler) serveVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createVault(w, r, rp)
	case http.MethodPatch:
		h.updateVault(w, r, rp)
	case http.MethodGet:
		h.getVault(w, r, rp)
	case http.MethodDelete:
		h.deleteVault(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req vaultRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := vaultInputFromRequest(&req)

	v, created, err := h.store.CreateOrUpdateVault(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toVaultResponse(&v))
}

// updateVault applies an ARM PATCH: the supplied tags replace the set, sku and
// identity are re-resolved when named, and the properties block is merged. A
// PATCH on a missing vault is a 404.
func (h *Handler) updateVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req vaultRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := vaultInputFromRequest(&req)

	v, err := h.store.UpdateVault(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVaultResponse(&v))
}

func (h *Handler) getVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	v, err := h.store.GetVault(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVaultResponse(&v))
}

func (h *Handler) deleteVault(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteVault(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listVaults(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []recoveryservices.Vault
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListVaultsByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListVaultsBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := vaultListResponse{Value: make([]vaultResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toVaultResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// writeDeleteStatus writes the idempotent ARM DELETE result: 200 when the
// resource existed, 204 when it did not.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeChildErr maps a child create error, translating a missing parent vault
// (NotFound) into the ARM ParentResourceNotFound 404 real Azure returns.
func writeChildErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteCErr(w, err)
}

// propertiesWith returns the raw properties object with key set to value, so a
// computed field (provisioningState) is surfaced without the store holding it in
// the raw block. A malformed or empty block yields an object carrying just that
// key.
func propertiesWith(properties json.RawMessage, key, value string) json.RawMessage {
	obj := map[string]json.RawMessage{}
	if len(properties) > 0 {
		if err := json.Unmarshal(properties, &obj); err != nil {
			obj = map[string]json.RawMessage{}
		}
	}

	valJSON, err := json.Marshal(value)
	if err != nil {
		return properties
	}

	obj[key] = valJSON

	raw, err := json.Marshal(obj)
	if err != nil {
		return properties
	}

	return raw
}

// userAssignedValue synthesizes the deterministic {principalId, clientId} block
// Azure returns for an assigned user identity.
func userAssignedValue(uaID string) json.RawMessage {
	principal := idgen.SyntheticGUID("recoveryservices/ua-principal/" + strings.ToLower(uaID))
	client := idgen.SyntheticGUID("recoveryservices/ua-client/" + strings.ToLower(uaID))

	raw, err := json.Marshal(map[string]string{"principalId": principal, "clientId": client})
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return raw
}

// Package storageaccount implements the Azure Storage-account ARM control plane
// (Microsoft.Storage/storageAccounts) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage
// AccountsClient clients configured with a custom endpoint hit this handler the
// same way they hit management.azure.com.
//
// This is the management-plane counterpart to the blob data-plane handler: an
// account name maps to a driver bucket, and the account's SKU / kind /
// access-tier cost attributes are stored via the driver's optional
// BucketAttributes capability so a discovery + cost consumer can price it.
//
// Coverage:
//
//	PUT    .../providers/Microsoft.Storage/storageAccounts/{name} — create/update
//	GET    .../providers/Microsoft.Storage/storageAccounts/{name} — get
//	PATCH  .../providers/Microsoft.Storage/storageAccounts/{name} — partial update
//	DELETE .../providers/Microsoft.Storage/storageAccounts/{name} — delete
//	GET/PUT .../storageAccounts/{name}/blobServices/default — blob service properties
//	                                       (versioning/soft-delete/change-feed/CORS)
//
// Create is a long-running operation in real Azure; the emulator completes it
// synchronously by returning 200 with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package storageaccount

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	providerName    = "Microsoft.Storage"
	resourceType    = "storageAccounts"
	defaultLocation = "eastus"

	skuStandardPrefix = "Standard"
	skuPremiumPrefix  = "Premium"
)

// attrBackend is the optional storage-account attribute capability. The Azure
// blob mock implements it; S3/GCS buckets don't (their ARM control plane is not
// served here anyway).
type attrBackend interface {
	SetBucketAttributes(name string, attrs storagedriver.AccountAttributes)
	BucketAttributes(ctx context.Context, bucket string) (storagedriver.AccountAttributes, error)
	// UpdateBucketAttributes atomically applies fn to the stored attributes,
	// backing the PATCH (AccountsClient.Update) partial-update handler.
	UpdateBucketAttributes(
		ctx context.Context, bucket string, fn func(storagedriver.AccountAttributes) storagedriver.AccountAttributes,
	) (storagedriver.AccountAttributes, error)
}

// Handler serves Microsoft.Storage/storageAccounts ARM requests against a
// storage bucket driver.
type Handler struct {
	bucket     storagedriver.Bucket
	attrs      attrBackend                           // nil when the driver doesn't expose account attributes
	keys       storagedriver.StorageAccountKeys      // nil when the driver doesn't expose access keys
	blobSvc    storagedriver.BlobServiceConfig       // nil when the driver doesn't expose blob service properties
	encryption storagedriver.AccountEncryptionConfig // nil when the driver doesn't expose account encryption
}

// New returns a storage-account handler backed by b.
func New(b storagedriver.Bucket) *Handler {
	h := &Handler{bucket: b}
	if a, ok := b.(attrBackend); ok {
		h.attrs = a
	}

	if k, ok := b.(storagedriver.StorageAccountKeys); ok {
		h.keys = k
	}

	if bs, ok := b.(storagedriver.BlobServiceConfig); ok {
		h.blobSvc = bs
	}

	if e, ok := b.(storagedriver.AccountEncryptionConfig); ok {
		h.encryption = e
	}

	return h
}

// Matches claims only the ARM management path for storage accounts. It never
// claims the blob data-plane path (blob.core.windows.net-style URLs never start
// with /subscriptions/), so blob routing is undisturbed.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, resourceType)
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// An empty resource name is a collection path — a subscription- or
	// resource-group-scoped list (…/storageAccounts). Route it to the list
	// handler rather than rejecting it, so a management-plane inventory sees
	// accounts created by PUT (matching real Azure and the disks/vnet handlers).
	if rp.ResourceName == "" {
		h.serveCollection(w, r, &rp)
		return
	}

	// GET/PUT .../storageAccounts/{name}/blobServices/default —
	// BlobServicesClient GetServiceProperties/SetServiceProperties. This is a
	// distinct sub-resource from the account itself: it must never fall through
	// to createOrUpdate, which would silently wipe the account's SKU/properties
	// on every "enable versioning" call.
	if strings.EqualFold(rp.SubResource, "blobServices") {
		h.serveBlobService(w, r, &rp)
		return
	}

	// POST .../storageAccounts/{name}/{action} — key management (listKeys,
	// regenerateKey). These carry a sub-resource action segment.
	if r.Method == http.MethodPost && rp.SubResource != "" {
		h.serveAction(w, r, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp)
	case http.MethodGet:
		h.get(w, r, &rp)
	case http.MethodPatch:
		h.update(w, r, &rp)
	case http.MethodDelete:
		h.deleteAccount(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// serveAction routes the POST action sub-resources (listKeys, regenerateKey).
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if !h.bucketExists(r.Context(), rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"storage account "+rp.ResourceName+" not found")
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case "listkeys":
		h.listKeys(w, r, rp)
	case "regeneratekey":
		h.regenerateKey(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unknown action "+rp.SubResource)
	}
}

// listKeys serves POST .../storageAccounts/{name}/listKeys.
func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if h.keys == nil {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "key management not supported")
		return
	}

	keys, err := h.keys.ListStorageAccountKeys(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMKeyList(keys))
}

// regenerateKey serves POST .../storageAccounts/{name}/regenerateKey.
func (h *Handler) regenerateKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if h.keys == nil {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "key management not supported")
		return
	}

	var body armRegenerateKey
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	keys, err := h.keys.RegenerateStorageAccountKey(r.Context(), rp.ResourceName, body.KeyName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMKeyList(keys))
}

// toARMKeyList maps driver keys to the ARM StorageAccountListKeysResult shape.
func toARMKeyList(keys []storagedriver.AccountKey) armKeyList {
	out := armKeyList{Keys: make([]armKey, 0, len(keys))}
	for _, k := range keys {
		out.Keys = append(out.Keys, armKey{
			KeyName: k.KeyName, Value: k.Value, Permissions: k.Permissions, CreationTime: k.CreationTime,
		})
	}

	return out
}

// serveCollection lists storage accounts at the subscription or resource-group
// scope (GET …/storageAccounts). Real Azure returns every account in scope in a
// {"value":[…]} envelope; the emulator is single-estate, so it lists every
// stored account under the requested scope.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armAccount, 0, len(buckets))

	for i := range buckets {
		scope := *rp
		scope.ResourceName = buckets[i].Name
		// A subscription-scoped list carries no resource group; the mock doesn't
		// track which group a bucket was created under, so stamp the default
		// group rather than emitting an id with an empty "resourceGroups//".
		if scope.ResourceGroup == "" {
			scope.ResourceGroup = "default"
		}

		out = append(out, h.toARMAccount(r.Context(), &scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, armAccountList{Value: out})
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armAccountCreate
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	name := rp.ResourceName

	// Upsert: an existing account (bucket) re-applies its cost attributes rather
	// than erroring, matching real Azure's create-or-update semantics.
	if err := h.bucket.CreateBucket(r.Context(), name); err != nil && !cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	attrs := storagedriver.AccountAttributes{Kind: body.Kind, Location: body.Location, Tags: body.Tags}
	if body.SKU != nil {
		attrs.SKU = body.SKU.Name
	}

	var encryption storagedriver.AccountEncryption

	if body.Properties != nil {
		attrs.AccessTier = body.Properties.AccessTier
		encryption = fromARMEncryptionReq(body.Properties.Encryption)
	}

	if h.attrs != nil {
		h.attrs.SetBucketAttributes(name, attrs)
	}

	// Full-replace like the rest of the account attributes above: a create-or-
	// update body with no encryption block resets to the platform-managed
	// default, matching how SKU/kind/tags behave on this same call.
	if h.encryption != nil {
		h.encryption.SetAccountEncryption(name, encryption)
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if !h.bucketExists(r.Context(), rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"storage account "+rp.ResourceName+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp))
}

// update serves PATCH …/storageAccounts/{name} (AccountsClient.Update),
// applying a non-destructive partial update: only the fields present in the
// request body (tags/sku/kind/accessTier/encryption) change, everything else
// on the account is preserved. The mutation runs through attrBackend's atomic
// UpdateBucketAttributes rather than a read-then-write pair, so a concurrent
// PATCH never loses an update.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if !h.bucketExists(r.Context(), rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"storage account "+rp.ResourceName+" not found")
		return
	}

	var body armAccountUpdate
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if h.attrs != nil {
		if _, err := h.attrs.UpdateBucketAttributes(r.Context(), rp.ResourceName, func(
			cur storagedriver.AccountAttributes,
		) storagedriver.AccountAttributes {
			return applyAccountUpdate(cur, &body)
		}); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	// Encryption lives in its own store (see AccountEncryptionConfig), so only
	// touch it when the PATCH actually submitted an encryption block —
	// otherwise a PATCH that only changes, say, tags would blindly reset any
	// previously configured customer-managed key.
	if h.encryption != nil && body.Properties != nil && body.Properties.Encryption != nil {
		if enc := fromARMEncryptionReq(body.Properties.Encryption); enc.KeySource != "" {
			h.encryption.SetAccountEncryption(rp.ResourceName, enc)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toARMAccount(r.Context(), rp))
}

// applyAccountUpdate merges the fields present on a PATCH body onto cur,
// leaving every field the request omitted untouched.
func applyAccountUpdate(cur storagedriver.AccountAttributes, body *armAccountUpdate) storagedriver.AccountAttributes {
	if body.Kind != "" {
		cur.Kind = body.Kind
	}

	if body.SKU != nil && body.SKU.Name != "" {
		cur.SKU = body.SKU.Name
	}

	if body.Tags != nil {
		cur.Tags = body.Tags
	}

	if body.Properties != nil && body.Properties.AccessTier != "" {
		cur.AccessTier = body.Properties.AccessTier
	}

	return cur
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.bucket.DeleteBucket(r.Context(), rp.ResourceName); err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) bucketExists(ctx context.Context, name string) bool {
	buckets, err := h.bucket.ListBuckets(ctx)
	if err != nil {
		return false
	}

	for _, b := range buckets {
		if b.Name == name {
			return true
		}
	}

	return false
}

// toARMAccount renders the ARM storage-account wire shape, reading the stored
// attributes (SKU / kind / access tier / location / tags) back through the
// driver.
func (h *Handler) toARMAccount(ctx context.Context, rp *azurearm.ResourcePath) armAccount {
	attrs := storagedriver.AccountAttributes{SKU: "Standard_LRS", Kind: "StorageV2", AccessTier: "Hot"}
	if h.attrs != nil {
		if a, err := h.attrs.BucketAttributes(ctx, rp.ResourceName); err == nil {
			attrs = a
		}
	}

	location := attrs.Location
	if location == "" {
		location = defaultLocation
	}

	var encryption storagedriver.AccountEncryption
	if h.encryption != nil {
		if e, err := h.encryption.AccountEncryption(ctx, rp.ResourceName); err == nil {
			encryption = e
		}
	}

	props := &armAccountProps{
		AccessTier:        attrs.AccessTier,
		ProvisioningState: "Succeeded",
		PrimaryLocation:   location,
		StatusOfPrimary:   "available",
		CreationTime:      h.accountCreatedAt(ctx, rp.ResourceName),
		PrimaryEndpoints:  accountEndpoints(rp.ResourceName),
		Encryption:        armEncryptionFor(encryption),
	}

	// GRS/RA-GRS/GZRS/RA-GZRS replicate to a documented paired region;
	// secondaryEndpoints is only readable (and thus only reported) on the
	// read-access (RA-*) variants — matching real Azure.
	if skuIsGeoRedundant(attrs.SKU) {
		if secondary := secondaryLocationFor(location); secondary != "" {
			props.SecondaryLocation = secondary
			props.StatusOfSecondary = "available"

			if skuIsReadAccessGeoRedundant(attrs.SKU) {
				props.SecondaryEndpoints = secondaryAccountEndpoints(rp.ResourceName)
			}
		}
	}

	return armAccount{
		ID:         azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName),
		Name:       rp.ResourceName,
		Type:       providerName + "/" + resourceType,
		Location:   location,
		Kind:       attrs.Kind,
		Tags:       attrs.Tags,
		SKU:        &armSKU{Name: attrs.SKU, Tier: skuTier(attrs.SKU)},
		Properties: props,
	}
}

// accountEndpoints builds the primaryEndpoints service URLs for an account.
func accountEndpoints(account string) *armEndpoints {
	return &armEndpoints{
		Blob:  "https://" + account + ".blob.core.windows.net/",
		Queue: "https://" + account + ".queue.core.windows.net/",
		Table: "https://" + account + ".table.core.windows.net/",
		File:  "https://" + account + ".file.core.windows.net/",
	}
}

// secondaryAccountEndpoints builds the secondaryEndpoints service URLs a
// read-access geo-redundant (RA-GRS/RA-GZRS) account reports: the real Azure
// convention appends "-secondary" to the account name on the same domains.
func secondaryAccountEndpoints(account string) *armEndpoints {
	secondary := account + "-secondary"

	return &armEndpoints{
		Blob:  "https://" + secondary + ".blob.core.windows.net/",
		Queue: "https://" + secondary + ".queue.core.windows.net/",
		Table: "https://" + secondary + ".table.core.windows.net/",
		File:  "https://" + secondary + ".file.core.windows.net/",
	}
}

// skuIsGeoRedundant reports whether sku replicates to a secondary region
// (Standard_GRS, Standard_RAGRS, Standard_GZRS, Standard_RAGZRS).
func skuIsGeoRedundant(sku string) bool {
	return strings.Contains(sku, "GRS") || strings.Contains(sku, "GZRS")
}

// skuIsReadAccessGeoRedundant reports whether sku additionally exposes its
// secondary region for reads (the RA-GRS/RA-GZRS variants).
func skuIsReadAccessGeoRedundant(sku string) bool {
	return strings.Contains(sku, "RAGRS") || strings.Contains(sku, "RAGZRS")
}

// secondaryLocationFor returns the documented geo-replication partner region
// for a primary location, or "" for a region with no listed pair. See
// https://learn.microsoft.com/en-us/azure/reliability/regions-paired
func secondaryLocationFor(location string) string {
	pairs := map[string]string{
		"eastus": "westus", "westus": "eastus",
		"eastus2": "centralus", "centralus": "eastus2",
		"westus2": "westcentralus", "westcentralus": "westus2",
		"westus3":        "eastus",
		"northcentralus": "southcentralus", "southcentralus": "northcentralus",
		"canadacentral": "canadaeast", "canadaeast": "canadacentral",
		"brazilsouth":        "southcentralus",
		"northeurope":        "westeurope",
		"westeurope":         "northeurope",
		"uksouth":            "ukwest",
		"ukwest":             "uksouth",
		"germanywestcentral": "germanynorth",
		"switzerlandnorth":   "switzerlandwest",
		"swedencentral":      "swedensouth",
		"norwayeast":         "norwaywest",
		"japaneast":          "japanwest", "japanwest": "japaneast",
		"koreacentral": "koreasouth", "koreasouth": "koreacentral",
		"australiaeast": "australiasoutheast", "australiasoutheast": "australiaeast",
		"centralindia": "southindia", "southindia": "centralindia",
		"westindia":        "southindia",
		"eastasia":         "southeastasia",
		"southeastasia":    "eastasia",
		"uaenorth":         "uaecentral",
		"southafricanorth": "southafricawest",
	}

	return pairs[strings.ToLower(strings.ReplaceAll(location, " ", ""))]
}

// armEncryptionFor renders the account encryption response block: the
// always-on platform-managed default (Microsoft.Storage), or, when the
// account was created/updated with a customer-managed key, the
// Microsoft.Keyvault key source and its key-vault properties — echoed back
// exactly as requested instead of always reporting the platform default.
func armEncryptionFor(enc storagedriver.AccountEncryption) *armEncryption {
	if enc.KeySource != "Microsoft.Keyvault" {
		return defaultEncryption()
	}

	svc := &armEncryptionService{Enabled: true, KeyType: "Account"}
	e := &armEncryption{
		KeySource: "Microsoft.Keyvault",
		Services:  &armEncryptionServices{Blob: svc, File: svc},
	}

	if enc.KeyVaultURI != "" || enc.KeyName != "" || enc.KeyVersion != "" {
		e.KeyVaultProperties = &armKeyVaultProperties{
			KeyVaultURI: enc.KeyVaultURI,
			KeyName:     enc.KeyName,
			KeyVersion:  enc.KeyVersion,
		}
	}

	return e
}

// fromARMEncryptionReq maps a create/update request's encryption block to the
// driver's AccountEncryption. A nil or keySource-less request yields the zero
// value, which armEncryptionFor renders as the platform-managed default.
func fromARMEncryptionReq(req *armEncryptionReq) storagedriver.AccountEncryption {
	if req == nil || req.KeySource == "" {
		return storagedriver.AccountEncryption{}
	}

	enc := storagedriver.AccountEncryption{KeySource: req.KeySource}
	if req.KeyVaultProperties != nil {
		enc.KeyVaultURI = req.KeyVaultProperties.KeyVaultURI
		enc.KeyName = req.KeyVaultProperties.KeyName
		enc.KeyVersion = req.KeyVaultProperties.KeyVersion
	}

	return enc
}

// defaultEncryption returns the always-on service-side encryption block real
// Azure storage accounts report when no customer-managed key is configured.
func defaultEncryption() *armEncryption {
	svc := &armEncryptionService{Enabled: true, KeyType: "Account"}

	return &armEncryption{
		KeySource: "Microsoft.Storage",
		Services:  &armEncryptionServices{Blob: svc, File: svc},
	}
}

// accountCreatedAt returns the account's creation timestamp from the backing
// bucket, falling back to a stable default when unavailable.
func (h *Handler) accountCreatedAt(ctx context.Context, name string) string {
	const fallback = "2020-01-01T00:00:00.0000000Z"

	buckets, err := h.bucket.ListBuckets(ctx)
	if err != nil {
		return fallback
	}

	for _, b := range buckets {
		if b.Name == name && b.CreatedAt != "" {
			return b.CreatedAt
		}
	}

	return fallback
}

// skuTier derives the read-only SKU tier from the SKU name (Standard_LRS ->
// Standard, Premium_LRS -> Premium).
func skuTier(sku string) string {
	switch {
	case strings.HasPrefix(sku, skuPremiumPrefix):
		return skuPremiumPrefix
	case strings.HasPrefix(sku, skuStandardPrefix):
		return skuStandardPrefix
	default:
		return ""
	}
}

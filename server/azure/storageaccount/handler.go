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
//	DELETE .../providers/Microsoft.Storage/storageAccounts/{name} — delete
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
}

// Handler serves Microsoft.Storage/storageAccounts ARM requests against a
// storage bucket driver.
type Handler struct {
	bucket storagedriver.Bucket
	attrs  attrBackend                      // nil when the driver doesn't expose account attributes
	keys   storagedriver.StorageAccountKeys // nil when the driver doesn't expose access keys
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

	if body.Properties != nil {
		attrs.AccessTier = body.Properties.AccessTier
	}

	if h.attrs != nil {
		h.attrs.SetBucketAttributes(name, attrs)
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

	return armAccount{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Kind:     attrs.Kind,
		Tags:     attrs.Tags,
		SKU:      &armSKU{Name: attrs.SKU, Tier: skuTier(attrs.SKU)},
		Properties: &armAccountProps{
			AccessTier:        attrs.AccessTier,
			ProvisioningState: "Succeeded",
			PrimaryLocation:   location,
			StatusOfPrimary:   "available",
			CreationTime:      h.accountCreatedAt(ctx, rp.ResourceName),
			PrimaryEndpoints:  accountEndpoints(rp.ResourceName),
			Encryption:        defaultEncryption(),
		},
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

// defaultEncryption returns the always-on service-side encryption block real
// Azure storage accounts report.
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

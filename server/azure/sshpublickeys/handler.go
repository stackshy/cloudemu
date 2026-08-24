// Package sshpublickeys serves Azure ARM Microsoft.Compute/sshPublicKeys
// requests against the underlying compute driver's KeyPair operations.
package sshpublickeys

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "sshPublicKeys"
	armNameTag      = "cloudemu:azureSSHKeyName"
	rgTag           = "cloudemu:azureRG"
	publicKeyTag    = "cloudemu:publicKey"
	defaultLocation = "eastus"
)

// Handler serves Microsoft.Compute/sshPublicKeys requests.
type Handler struct {
	compute computedriver.Compute
}

// New returns an SSH public keys handler.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// /sshPublicKeys/{name}/generateKeyPair is a POST sub-resource action.
	if strings.EqualFold(rp.SubResource, "generateKeyPair") {
		h.generateKeyPair(w, r, rp)
		return
	}

	if rp.ResourceName == "" {
		h.serveCollection(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, rp)
	case http.MethodPatch:
		h.update(w, r, rp)
	case http.MethodGet:
		h.get(w, r, rp)
	case http.MethodDelete:
		h.delete(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method+" "+r.URL.Path)

		return
	}

	keys, err := h.compute.DescribeKeyPairs(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]sshKeyResponse, 0, len(keys))

	for i := range keys {
		if rp.ResourceGroup != "" && tagOr(keys[i].Tags, rgTag, "") != rp.ResourceGroup {
			continue
		}

		name := tagOr(keys[i].Tags, armNameTag, keys[i].Name)
		scope := rp
		scope.ResourceName = name
		out = append(out, toSSHKeyResponse(&keys[i], scope, ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, sshKeyListResponse{Value: out})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req sshKeyRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cfg := computedriver.KeyPairConfig{
		Name:    rp.ResourceName,
		KeyType: "rsa",
		Tags:    mergeTags(req.Tags, rp.ResourceName, req.Properties.PublicKey, rp.ResourceGroup),
	}

	// ARM CreateOrUpdate is idempotent: a repeated PUT replaces the resource
	// in place rather than failing with AlreadyExists.
	if _, err := findKeyByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); err == nil {
		_ = h.compute.DeleteKeyPair(r.Context(), rp.ResourceName)
	}

	key, err := h.compute.CreateKeyPair(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	body := toSSHKeyResponse(key, rp, location)
	if req.Properties.PublicKey != "" {
		body.Properties.PublicKey = req.Properties.PublicKey
	}

	// SDK is happiest with sync 200/201 for sshPublicKeys.
	azurearm.WriteJSON(w, http.StatusOK, body)
}

// update handles PATCH .../sshPublicKeys/{name}. It updates the resource's
// publicKey and/or tags in place (Azure SshPublicKeys Update). A publicKey
// omitted from the body leaves the key material unchanged; a tags object
// present in the body replaces the resource's tags.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	updater, ok := h.compute.(computedriver.AzureSSHKeyUpdater)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "sshPublicKeys update not supported")
		return
	}

	existing, err := findKeyByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req sshKeyRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	// PATCH semantics: an omitted publicKey keeps the current key material; an
	// omitted tags object keeps the current tags.
	publicKey := tagOr(existing.Tags, publicKeyTag, existing.PublicKey)
	if req.Properties.PublicKey != "" {
		publicKey = req.Properties.PublicKey
	}

	userTags := stripInternalTags(existing.Tags)
	if req.Tags != nil {
		userTags = req.Tags
	}

	merged := mergeTags(userTags, rp.ResourceName, publicKey, rp.ResourceGroup)

	updated, err := updater.UpdateKeyPair(r.Context(), rp.ResourceName, &publicKey, merged)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSSHKeyResponse(updated, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	key, err := findKeyByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSSHKeyResponse(key, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	key, err := findKeyByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.DeleteKeyPair(r.Context(), key.Name); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// generateKeyPair handles POST .../sshPublicKeys/{name}/generateKeyPair. Real
// Azure generates a fresh 2048-bit RSA pair server-side, stores the public key
// on the resource, and returns both keys (the only time the private key is
// disclosed). We delegate to the driver's KeyPairGenerator when available.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) generateKeyPair(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method)

		return
	}

	gen, ok := h.compute.(computedriver.KeyPairGenerator)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"generateKeyPair not supported")

		return
	}

	key, err := gen.GenerateKeyPair(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, generateKeyPairResponse{
		ID:         azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName),
		PublicKey:  key.PublicKey,
		PrivateKey: key.PrivateKey,
	})
}

func findKeyByName(ctx context.Context, c computedriver.Compute, resourceGroup, name string) (*computedriver.KeyPairInfo, error) {
	keys, err := c.DescribeKeyPairs(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range keys {
		if tagOr(keys[i].Tags, armNameTag, keys[i].Name) != name {
			continue
		}

		if resourceGroup != "" && tagOr(keys[i].Tags, rgTag, "") != resourceGroup {
			continue
		}

		return &keys[i], nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "sshPublicKey %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toSSHKeyResponse(key *computedriver.KeyPairInfo, rp azurearm.ResourcePath, location string) sshKeyResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(key.Tags, armNameTag, key.Name)

	pub := tagOr(key.Tags, publicKeyTag, key.PublicKey)

	return sshKeyResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Tags:     stripInternalTags(key.Tags),
		Properties: sshKeyResponseProps{
			PublicKey: pub,
		},
	}
}

// extraSlots is the size headroom we add when copying a tag map and inserting
// the cloudemu-internal name, resource-group, and public-key tags.
const extraSlots = 3

func mergeTags(in map[string]string, name, publicKey, resourceGroup string) map[string]string {
	out := make(map[string]string, len(in)+extraSlots)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = name

	if resourceGroup != "" {
		out[rgTag] = resourceGroup
	}

	if publicKey != "" {
		out[publicKeyTag] = publicKey
	}

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

func stripInternalTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == armNameTag || k == publicKeyTag || k == rgTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

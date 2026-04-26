// Package sshpublickeys serves Azure ARM Microsoft.Compute/sshPublicKeys
// requests against the underlying compute driver's KeyPair operations.
package sshpublickeys

import (
	"context"
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName    = "Microsoft.Compute"
	resourceType    = "sshPublicKeys"
	armNameTag      = "cloudemu:azureSSHKeyName"
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
		Tags:    mergeTags(req.Tags, rp.ResourceName, req.Properties.PublicKey),
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

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	key, err := findKeyByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toSSHKeyResponse(key, rp, ""))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	key, err := findKeyByName(r.Context(), h.compute, rp.ResourceName)
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

// generateKeyPair handles POST .../sshPublicKeys/{name}/generateKeyPair.
// Real Azure generates a fresh RSA pair server-side; we return the driver's
// provisioned PublicKey/PrivateKey, which the test fixtures populate.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) generateKeyPair(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"not implemented: "+r.Method)

		return
	}

	key, err := findKeyByName(r.Context(), h.compute, rp.ResourceName)
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

func findKeyByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.KeyPairInfo, error) {
	keys, err := c.DescribeKeyPairs(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range keys {
		if tagOr(keys[i].Tags, armNameTag, keys[i].Name) == name {
			return &keys[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "sshPublicKey %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toSSHKeyResponse(key *computedriver.KeyPairInfo, rp azurearm.ResourcePath, location string) sshKeyResponse {
	if location == "" {
		location = defaultLocation
	}

	name := tagOr(key.Tags, armNameTag, key.Name)

	pub := tagOr(key.Tags, "cloudemu:publicKey", key.PublicKey)

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
// the cloudemu-internal name + public-key tags.
const extraSlots = 2

func mergeTags(in map[string]string, name, publicKey string) map[string]string {
	out := make(map[string]string, len(in)+extraSlots)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = name

	if publicKey != "" {
		out["cloudemu:publicKey"] = publicKey
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
		if k == armNameTag || k == "cloudemu:publicKey" {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

package storageaccount

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	containerResourceType = providerName + "/" + resourceType + "/blobServices/containers"

	// defaultEncryptionScope is the scope real Azure reports for a container
	// created without one: the account's own encryption key.
	defaultEncryptionScope = "$account-encryption-key"

	publicAccessNone = "None"
	leaseAvailable   = "Available"
	leaseUnlocked    = "Unlocked"
)

// containerBackend is the container metadata and access-level surface the ARM
// blob container resource reads and writes. The Azure blob mock implements it
// as part of AzureBlobExtensions.
type containerBackend interface {
	SetContainerMetadata(ctx context.Context, container string, metadata map[string]string) error
	ContainerMetadata(ctx context.Context, container string) (map[string]string, error)
	SetContainerAccessPolicy(ctx context.Context, container, publicAccess string, policies []storagedriver.SignedIdentifier) error
	ContainerAccessPolicy(ctx context.Context, container string) (string, []storagedriver.SignedIdentifier, error)
}

// armContainer is the ARM BlobContainer / ListContainerItem wire shape.
type armContainer struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Type       string                  `json:"type"`
	Etag       string                  `json:"etag,omitempty"`
	Properties *armContainerProperties `json:"properties"`
}

// armContainerProperties is the ARM ContainerProperties block. The read-only
// lease, immutability and soft-delete fields always carry their real values
// for a container with no lease, policy or hold.
type armContainerProperties struct {
	PublicAccess                string            `json:"publicAccess,omitempty"`
	Metadata                    map[string]string `json:"metadata,omitempty"`
	DefaultEncryptionScope      string            `json:"defaultEncryptionScope,omitempty"`
	DenyEncryptionScopeOverride *bool             `json:"denyEncryptionScopeOverride,omitempty"`

	LastModifiedTime       string `json:"lastModifiedTime,omitempty"`
	LeaseState             string `json:"leaseState,omitempty"`
	LeaseStatus            string `json:"leaseStatus,omitempty"`
	HasImmutabilityPolicy  *bool  `json:"hasImmutabilityPolicy,omitempty"`
	HasLegalHold           *bool  `json:"hasLegalHold,omitempty"`
	Deleted                *bool  `json:"deleted,omitempty"`
	RemainingRetentionDays *int   `json:"remainingRetentionDays,omitempty"`
}

// armContainerRequest is the PUT/PATCH body: only properties are settable.
type armContainerRequest struct {
	Properties *armContainerProperties `json:"properties,omitempty"`
}

// armContainerList is the {"value":[…]} envelope of a container list.
type armContainerList struct {
	Value []armContainer `json:"value"`
}

// serveContainerCollection serves GET …/blobServices/default/containers
// (BlobContainersClient.NewListPager).
func (h *Handler) serveContainerCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	acct, ok := h.lookupForContainers(w, r, rp)
	if !ok {
		return
	}

	list, err := h.accounts.ListAccountContainers(r.Context(), acct.Name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := armContainerList{Value: make([]armContainer, 0, len(list))}

	for i := range list {
		c, err := h.renderContainer(r.Context(), rp, acct.Name, list[i].Name, list[i].CreatedAt)
		if err != nil {
			continue
		}

		out.Value = append(out.Value, c)
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// serveContainer serves one ARM blob container,
// …/blobServices/default/containers/{name}.
func (h *Handler) serveContainer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string) {
	acct, ok := h.lookupForContainers(w, r, rp)
	if !ok {
		return
	}

	key := storagedriver.AzureContainerKey(acct.Name, name)

	switch r.Method {
	case http.MethodPut:
		h.putContainer(w, r, rp, acct.Name, name)
	case http.MethodGet:
		h.writeContainer(w, r, rp, acct.Name, name, http.StatusOK)
	case http.MethodPatch:
		h.patchContainer(w, r, rp, acct.Name, name)
	case http.MethodDelete:
		if err := h.accounts.ForceDeleteContainer(r.Context(), key); err != nil {
			if cerrors.IsNotFound(err) {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			azurearm.WriteCErr(w, err)

			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// lookupForContainers resolves the owning account and checks the driver
// exposes the container surface.
func (h *Handler) lookupForContainers(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
) (storagedriver.StorageAccountRef, bool) {
	acct, ok := h.lookup(w, r, rp)
	if !ok {
		return acct, false
	}

	if h.containers == nil {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "blob containers not supported")
		return acct, false
	}

	return acct, true
}

// putContainer serves PUT (BlobContainersClient.Create): 201 for a new
// container, 200 when it already exists. The body's properties replace the
// container's public access and metadata.
func (h *Handler) putContainer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, account, name string) {
	if !storagedriver.ValidAzureContainerName(name) {
		azurearm.WriteError(w, http.StatusBadRequest, "ContainerOperationFailure", containerNameError(name))

		return
	}

	var body armContainerRequest
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	props := body.Properties
	if props == nil {
		props = &armContainerProperties{}
	}

	ctx := r.Context()
	key := storagedriver.AzureContainerKey(account, name)
	status := http.StatusCreated

	if err := h.bucket.CreateBucket(ctx, key); err != nil {
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		status = http.StatusOK
	}

	if err := h.applyContainerProps(ctx, key, props, true); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if status == http.StatusCreated || props.DefaultEncryptionScope != "" {
		if err := h.accounts.SetContainerEncryptionScope(ctx, key, encryptionScopeOf(props)); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	h.writeContainer(w, r, rp, account, name, status)
}

// encryptionScopeOf reads the encryption scope settings of a PUT body. The
// scope is fixed at create unless a later PUT names one explicitly.
func encryptionScopeOf(props *armContainerProperties) storagedriver.ContainerEncryptionScope {
	scope := storagedriver.ContainerEncryptionScope{DefaultEncryptionScope: props.DefaultEncryptionScope}
	if props.DenyEncryptionScopeOverride != nil {
		scope.DenyEncryptionScopeOverride = *props.DenyEncryptionScopeOverride
	}

	return scope
}

// patchContainer serves PATCH (BlobContainersClient.Update): only the public
// access level and metadata present in the body change.
func (h *Handler) patchContainer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, account, name string) {
	var body armContainerRequest
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := storagedriver.AzureContainerKey(account, name)

	if _, _, err := h.containers.ContainerAccessPolicy(r.Context(), key); err != nil {
		writeContainerErr(w, err)
		return
	}

	if body.Properties != nil {
		if err := h.applyContainerProps(r.Context(), key, body.Properties, false); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	h.writeContainer(w, r, rp, account, name, http.StatusOK)
}

// applyContainerProps writes public access and metadata. With replace, an
// omitted field resets to its default (PUT); otherwise it is left as is
// (PATCH). Stored access policies are kept either way.
func (h *Handler) applyContainerProps(ctx context.Context, key string, props *armContainerProperties, replace bool) error {
	if replace || props.PublicAccess != "" {
		_, policies, err := h.containers.ContainerAccessPolicy(ctx, key)
		if err != nil {
			return err
		}

		if err := h.containers.SetContainerAccessPolicy(ctx, key, toDataPlaneAccess(props.PublicAccess), policies); err != nil {
			return err
		}
	}

	if replace || props.Metadata != nil {
		if err := h.containers.SetContainerMetadata(ctx, key, props.Metadata); err != nil {
			return err
		}
	}

	return nil
}

// writeContainer renders the container, or the real 404 when it is missing.
func (h *Handler) writeContainer(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, account, name string, status int,
) {
	createdAt := ""

	list, err := h.accounts.ListAccountContainers(r.Context(), account)
	if err == nil {
		for i := range list {
			if list[i].Name == name {
				createdAt = list[i].CreatedAt
			}
		}
	}

	c, err := h.renderContainer(r.Context(), rp, account, name, createdAt)
	if err != nil {
		writeContainerErr(w, err)
		return
	}

	azurearm.WriteJSON(w, status, c)
}

// renderContainer builds the ARM container shape from the stored state, with
// the real defaults for every unset property.
func (h *Handler) renderContainer(
	ctx context.Context, rp *azurearm.ResourcePath, account, name, createdAt string,
) (armContainer, error) {
	key := storagedriver.AzureContainerKey(account, name)

	access, _, err := h.containers.ContainerAccessPolicy(ctx, key)
	if err != nil {
		return armContainer{}, err
	}

	metadata, err := h.containers.ContainerMetadata(ctx, key)
	if err != nil {
		return armContainer{}, err
	}

	scope, err := h.accounts.ContainerEncryptionScope(ctx, key)
	if err != nil {
		return armContainer{}, err
	}

	f, zero := false, 0

	props := &armContainerProperties{
		PublicAccess:                toARMAccess(access),
		Metadata:                    metadata,
		DefaultEncryptionScope:      strOr(scope.DefaultEncryptionScope, defaultEncryptionScope),
		DenyEncryptionScopeOverride: &scope.DenyEncryptionScopeOverride,
		LastModifiedTime:            createdAt,
		LeaseState:                  leaseAvailable,
		LeaseStatus:                 leaseUnlocked,
		HasImmutabilityPolicy:       &f,
		HasLegalHold:                &f,
		Deleted:                     &f,
		RemainingRetentionDays:      &zero,
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, account) +
		"/blobServices/default/containers/" + name

	return armContainer{
		ID: id, Name: name, Type: containerResourceType,
		Etag:       containerEtag(name, createdAt, props),
		Properties: props,
	}, nil
}

// containerEtag derives a stable etag from the container's settable state, so
// it is identical across reads and changes when the container is updated.
func containerEtag(name, createdAt string, props *armContainerProperties) string {
	keys := make([]string, 0, len(props.Metadata))
	for k := range props.Metadata {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	b.WriteString(name + "|" + createdAt + "|" + props.PublicAccess + "|" + props.DefaultEncryptionScope)

	for _, k := range keys {
		b.WriteString("|" + k + "=" + props.Metadata[k])
	}

	sum := sha256.Sum256([]byte(b.String()))

	return fmt.Sprintf("\"0x8D%X\"", sum[:6])
}

// writeContainerErr maps a missing container to the real 404 ContainerNotFound.
// containerNameError returns the real storage RP message for an invalid
// container name: a name of the wrong length is reported apart from one with
// characters outside the container name alphabet.
func containerNameError(name string) string {
	const minLen, maxLen = 3, 63

	if len(name) < minLen || len(name) > maxLen {
		return "The specified resource name length is not within the permissible limits."
	}

	return "The specified resource name contains invalid characters."
}

func writeContainerErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteError(w, http.StatusNotFound, "ContainerNotFound", "The specified container does not exist.")
		return
	}

	azurearm.WriteCErr(w, err)
}

// toDataPlaneAccess maps the ARM publicAccess enum (None/Blob/Container) to the
// data-plane x-ms-blob-public-access value the blob store keeps ("" for
// private, "blob", "container").
func toDataPlaneAccess(armAccess string) string {
	if armAccess == "" || strings.EqualFold(armAccess, publicAccessNone) {
		return ""
	}

	return strings.ToLower(armAccess)
}

// toARMAccess is the inverse of toDataPlaneAccess.
func toARMAccess(access string) string {
	switch strings.ToLower(access) {
	case "blob":
		return "Blob"
	case "container":
		return "Container"
	default:
		return publicAccessNone
	}
}

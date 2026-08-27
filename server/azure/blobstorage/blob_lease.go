package blobstorage

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// x-ms-lease-action values dispatched by leaseBlob.
const (
	leaseActionAcquire = "acquire"
	leaseActionRenew   = "renew"
	leaseActionChange  = "change"
	leaseActionRelease = "release"
	leaseActionBreak   = "break"
)

// checkLease validates the request's x-ms-lease-id header against any active
// lease on the blob (Put Blob, Delete Blob, Put Block, Put Block List, Set
// Blob Metadata/Properties, Append Block, and Copy Blob's destination all
// require a matching lease id while a blob is leased). It returns true,
// having already written the Azure lease error, when the request must be
// rejected. It's a no-op when the driver doesn't implement leases.
func (h *Handler) checkLease(w http.ResponseWriter, r *http.Request, container, blob string) bool {
	ext, ok := h.bucket.(storagedriver.AzureBlobExtensions)
	if !ok {
		return false
	}

	if err := ext.CheckBlobLease(r.Context(), container, blob, r.Header.Get("X-Ms-Lease-Id")); err != nil {
		writeErr(w, err)
		return true
	}

	return false
}

// leaseBlob handles PUT /{container}/{blob}?comp=lease, dispatching on
// x-ms-lease-action. See
// https://learn.microsoft.com/en-us/rest/api/storageservices/lease-blob.
func (h *Handler) leaseBlob(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	switch strings.ToLower(r.Header.Get("X-Ms-Lease-Action")) {
	case leaseActionAcquire:
		h.acquireLease(w, r, ext, container, blob)
	case leaseActionRenew:
		h.renewLease(w, r, ext, container, blob)
	case leaseActionChange:
		h.changeLease(w, r, ext, container, blob)
	case leaseActionRelease:
		h.releaseLease(w, r, ext, container, blob)
	case leaseActionBreak:
		h.breakLease(w, r, ext, container, blob)
	default:
		writeError(w, http.StatusBadRequest, "InvalidHeaderValue", "missing or invalid x-ms-lease-action")
	}
}

func (*Handler) acquireLease(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	durationHeader := r.Header.Get("X-Ms-Lease-Duration")
	if durationHeader == "" {
		writeError(w, http.StatusBadRequest, "MissingRequiredHeader", "x-ms-lease-duration is required for acquire")
		return
	}

	n, err := strconv.ParseInt(durationHeader, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidHeaderValue", "invalid x-ms-lease-duration")
		return
	}

	result, err := ext.AcquireLease(r.Context(), container, blob, int32(n), r.Header.Get("X-Ms-Proposed-Lease-Id"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeLeaseResult(w, result, http.StatusCreated)
}

func (*Handler) renewLease(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	result, err := ext.RenewLease(r.Context(), container, blob, r.Header.Get("X-Ms-Lease-Id"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeLeaseResult(w, result, http.StatusOK)
}

func (*Handler) changeLease(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	result, err := ext.ChangeLease(r.Context(), container, blob,
		r.Header.Get("X-Ms-Lease-Id"), r.Header.Get("X-Ms-Proposed-Lease-Id"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeLeaseResult(w, result, http.StatusOK)
}

func (*Handler) releaseLease(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	result, err := ext.ReleaseLease(r.Context(), container, blob, r.Header.Get("X-Ms-Lease-Id"))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeLeaseResult(w, result, http.StatusOK)
}

func (*Handler) breakLease(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	var period *int32

	if v := r.Header.Get("X-Ms-Lease-Break-Period"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "InvalidHeaderValue", "invalid x-ms-lease-break-period")
			return
		}

		p := int32(n)
		period = &p
	}

	leaseTime, err := ext.BreakLease(r.Context(), container, blob, period)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Lease-Time", strconv.FormatInt(int64(leaseTime), 10))
	w.WriteHeader(http.StatusAccepted)
}

// writeLeaseResult writes a successful Lease Blob acquire/renew/change/release
// response.
func writeLeaseResult(w http.ResponseWriter, result *storagedriver.BlobLeaseResult, status int) {
	if result != nil {
		if result.LeaseID != "" {
			w.Header().Set("X-Ms-Lease-Id", result.LeaseID)
		}

		if result.ETag != "" {
			w.Header().Set("ETag", fmt.Sprintf("%q", result.ETag))
		}

		if result.LastModified != "" {
			w.Header().Set("Last-Modified", httpDate(result.LastModified))
		}
	}

	w.WriteHeader(status)
}

// getBlockList handles GET /{container}/{blob}?comp=blocklist.
func (*Handler) getBlockList(w http.ResponseWriter, r *http.Request, ext blobExt, container, blob string) {
	committed, uncommitted, err := ext.GetBlockList(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := blockListResult{}
	for _, b := range committed {
		out.CommittedBlocks = append(out.CommittedBlocks, blockXML{Name: b.Name, Size: b.Size})
	}

	for _, b := range uncommitted {
		out.UncommittedBlocks = append(out.UncommittedBlocks, blockXML{Name: b.Name, Size: b.Size})
	}

	writeXML(w, out)
}

// setContainerACL handles PUT /{container}?restype=container&comp=acl (Set
// Container ACL).
func (h *Handler) setContainerACL(w http.ResponseWriter, r *http.Request, container string) {
	ext, ok := h.bucket.(blobExt)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "container ACL not supported")
		return
	}

	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	var identifiers []storagedriver.SignedIdentifier

	if len(bytes.TrimSpace(body)) > 0 {
		var wrapper signedIdentifiersXML
		if err := xml.Unmarshal(body, &wrapper); err != nil {
			writeError(w, http.StatusBadRequest, "InvalidXmlDocument", "could not parse SignedIdentifiers body")
			return
		}

		for _, id := range wrapper.Identifiers {
			identifiers = append(identifiers, storagedriver.SignedIdentifier{
				ID: id.ID, Start: id.AccessPolicy.Start, Expiry: id.AccessPolicy.Expiry, Permission: id.AccessPolicy.Permission,
			})
		}
	}

	publicAccess := r.Header.Get("X-Ms-Blob-Public-Access")

	if err := ext.SetContainerAccessPolicy(r.Context(), container, publicAccess, identifiers); err != nil {
		writeErr(w, err)
		return
	}

	created, _ := h.containerCreatedAt(r, container)
	w.Header().Set("ETag", containerETag(container, created))
	w.Header().Set("Last-Modified", httpDate(created))
	w.WriteHeader(http.StatusOK)
}

// getContainerACL handles GET /{container}?restype=container&comp=acl (Get
// Container ACL).
func (h *Handler) getContainerACL(w http.ResponseWriter, r *http.Request, container string) {
	ext, ok := h.bucket.(blobExt)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "container ACL not supported")
		return
	}

	publicAccess, identifiers, err := ext.ContainerAccessPolicy(r.Context(), container)
	if err != nil {
		writeErr(w, err)
		return
	}

	if publicAccess != "" {
		w.Header().Set("X-Ms-Blob-Public-Access", publicAccess)
	}

	created, _ := h.containerCreatedAt(r, container)
	w.Header().Set("ETag", containerETag(container, created))
	w.Header().Set("Last-Modified", httpDate(created))

	out := signedIdentifiersXML{}
	for _, id := range identifiers {
		out.Identifiers = append(out.Identifiers, signedIdentifierXML{
			ID:           id.ID,
			AccessPolicy: accessPolicyXML{Start: id.Start, Expiry: id.Expiry, Permission: id.Permission},
		})
	}

	writeXML(w, out)
}

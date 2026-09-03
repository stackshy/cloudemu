package blobstorage

import (
	"net/http"
	"strconv"
	"strings"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	headerImmutabilityUntil = "X-Ms-Immutability-Policy-Until-Date"
	headerImmutabilityMode  = "X-Ms-Immutability-Policy-Mode"
	headerLegalHold         = "X-Ms-Legal-Hold"
)

// setBlobImmutabilityPolicy handles PUT /{container}/{blob}?comp=immutabilityPolicies,
// setting the blob's time-based retention immutability policy (Set Blob
// Immutability Policy). The retain-until date comes from
// x-ms-immutability-policy-until-date (RFC1123) and the mode from
// x-ms-immutability-policy-mode (default Unlocked).
func (h *Handler) setBlobImmutabilityPolicy(w http.ResponseWriter, r *http.Request, container, blob string) {
	ext, ok := h.bucket.(storagedriver.AzureImmutableBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob immutability policies are not supported")
		return
	}

	untilRaw := r.Header.Get(headerImmutabilityUntil)
	if untilRaw == "" {
		writeError(w, http.StatusBadRequest, "MissingRequiredHeader",
			"x-ms-immutability-policy-until-date is required")

		return
	}

	until, err := http.ParseTime(strings.TrimSpace(untilRaw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidHeaderValue",
			"x-ms-immutability-policy-until-date must be an RFC1123 date")

		return
	}

	policy, err := ext.SetBlobImmutabilityPolicy(r.Context(), container, blob, storagedriver.BlobImmutabilityPolicy{
		ExpiryTime: until.UTC(),
		Mode:       normalizeImmutabilityMode(r.Header.Get(headerImmutabilityMode)),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeImmutabilityPolicyHeaders(w, policy)
	w.WriteHeader(http.StatusOK)
}

// deleteBlobImmutabilityPolicy handles DELETE /{container}/{blob}?comp=immutabilityPolicies,
// removing an Unlocked immutability policy (Delete Blob Immutability Policy).
func (h *Handler) deleteBlobImmutabilityPolicy(w http.ResponseWriter, r *http.Request, container, blob string) {
	ext, ok := h.bucket.(storagedriver.AzureImmutableBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob immutability policies are not supported")
		return
	}

	if err := ext.DeleteBlobImmutabilityPolicy(r.Context(), container, blob); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// setBlobLegalHold handles PUT /{container}/{blob}?comp=legalhold, setting or
// clearing the blob's legal hold (Set Blob Legal Hold) from x-ms-legal-hold.
func (h *Handler) setBlobLegalHold(w http.ResponseWriter, r *http.Request, container, blob string) {
	ext, ok := h.bucket.(storagedriver.AzureImmutableBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob legal hold is not supported")
		return
	}

	hold, err := strconv.ParseBool(strings.TrimSpace(r.Header.Get(headerLegalHold)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidHeaderValue", "x-ms-legal-hold must be true or false")
		return
	}

	if err := ext.SetBlobLegalHold(r.Context(), container, blob, hold); err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set(headerLegalHold, strconv.FormatBool(hold))
	w.WriteHeader(http.StatusOK)
}

// writeImmutabilityHeaders sets the immutable-storage response headers
// (x-ms-immutability-policy-until-date / -mode, x-ms-legal-hold) on a Get Blob /
// Get Blob Properties response when the driver tracks blob immutability and the
// blob carries a policy or hold. A missing capability or blob is silently
// skipped — the read itself still succeeds.
func (h *Handler) writeImmutabilityHeaders(w http.ResponseWriter, r *http.Request, container, blob string) {
	ext, ok := h.bucket.(storagedriver.AzureImmutableBlob)
	if !ok {
		return
	}

	policy, legalHold, err := ext.BlobImmutability(r.Context(), container, blob)
	if err != nil {
		return
	}

	writeImmutabilityPolicyHeaders(w, policy)

	if legalHold {
		w.Header().Set(headerLegalHold, strconv.FormatBool(legalHold))
	}
}

// writeImmutabilityPolicyHeaders emits the time-based policy headers for a
// non-empty policy, using the lowercase mode value real Azure returns.
func writeImmutabilityPolicyHeaders(w http.ResponseWriter, policy storagedriver.BlobImmutabilityPolicy) {
	if policy.Mode == "" || policy.ExpiryTime.IsZero() {
		return
	}

	w.Header().Set(headerImmutabilityUntil, policy.ExpiryTime.UTC().Format(http.TimeFormat))
	w.Header().Set(headerImmutabilityMode, strings.ToLower(policy.Mode))
}

// normalizeImmutabilityMode maps the x-ms-immutability-policy-mode header
// (case-insensitive, default Unlocked) to the driver's canonical mode value. An
// unrecognized value is passed through so the driver rejects it.
func normalizeImmutabilityMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "unlocked":
		return storagedriver.BlobImmutabilityUnlocked
	case "locked":
		return storagedriver.BlobImmutabilityLocked
	default:
		return v
	}
}

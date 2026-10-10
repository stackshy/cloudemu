package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// restoreRequestXML is the RestoreObject request body. The retrieval tier is
// GlacierJobParameters/Tier, or the top-level Tier.
type restoreRequestXML struct {
	XMLName      xml.Name `xml:"RestoreRequest"`
	Days         *int     `xml:"Days"`
	GlacierTier  string   `xml:"GlacierJobParameters>Tier"`
	Tier         string   `xml:"Tier"`
	Type         string   `xml:"Type"`
	OutputPrefix string   `xml:"OutputLocation>S3>Prefix"`
	OutputBucket string   `xml:"OutputLocation>S3>BucketName"`
}

// invalidObjectStateXML is the S3 InvalidObjectState error, which names the
// object's storage class.
type invalidObjectStateXML struct {
	XMLName      xml.Name `xml:"Error"`
	Code         string   `xml:"Code"`
	Message      string   `xml:"Message"`
	StorageClass string   `xml:"StorageClass,omitempty"`
}

// restoreStatusXML is the RestoreStatus of a listed object (ListObjects with
// x-amz-optional-object-attributes: RestoreStatus).
type restoreStatusXML struct {
	IsRestoreInProgress bool   `xml:"IsRestoreInProgress"`
	RestoreExpiryDate   string `xml:"RestoreExpiryDate,omitempty"`
}

// msgMalformedXML is the S3 MalformedXML message.
const msgMalformedXML = "The XML you provided was not well-formed or did not validate against our published schema"

// restoreObject answers POST /{bucket}/{key}?restore (RestoreObject): 202 when
// a restore starts, 200 when a completed restore only has its expiry reset.
func (h *Handler) restoreObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	restorer, ok := h.bucket.(driver.ObjectRestorer)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "RestoreObject is not supported")
		return
	}

	req, ok := parseRestoreRequest(w, r)
	if !ok {
		return
	}

	versionID := r.URL.Query().Get("versionId")

	accepted, err := restorer.RestoreObject(r.Context(), bucket, key, versionID, req)
	if err != nil {
		writeRestoreErr(w, err, versionID)
		return
	}

	if accepted {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// parseRestoreRequest decodes and checks a RestoreRequest body. It returns
// false after writing the error.
func parseRestoreRequest(w http.ResponseWriter, r *http.Request) (driver.RestoreRequest, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", msgMalformedXML)
		return driver.RestoreRequest{}, false
	}

	var doc restoreRequestXML
	if len(body) > 0 {
		if err := xml.Unmarshal(body, &doc); err != nil {
			writeError(w, http.StatusBadRequest, "MalformedXML", msgMalformedXML)
			return driver.RestoreRequest{}, false
		}
	}

	// S3 Select restores and restores to an output location are closed to new
	// customers and not modeled.
	if strings.EqualFold(doc.Type, "SELECT") || doc.OutputBucket != "" || doc.OutputPrefix != "" {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "SELECT restore requests are not supported")
		return driver.RestoreRequest{}, false
	}

	tier, ok := restoreTier(&doc)
	if !ok {
		writeError(w, http.StatusBadRequest, "MalformedXML", msgMalformedXML)
		return driver.RestoreRequest{}, false
	}

	req := driver.RestoreRequest{Tier: tier}
	if doc.Days != nil {
		req.Days = *doc.Days
		if req.Days <= 0 {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "Days must be a positive integer")
			return driver.RestoreRequest{}, false
		}
	}

	return req, true
}

// restoreTier is the request's retrieval tier: GlacierJobParameters/Tier, else
// the top-level Tier ("" when neither is set). ok is false for an unknown tier.
func restoreTier(doc *restoreRequestXML) (string, bool) {
	tier := doc.GlacierTier
	if tier == "" {
		tier = doc.Tier
	}

	switch tier {
	case "", driver.RestoreTierStandard, driver.RestoreTierBulk, driver.RestoreTierExpedited:
		return tier, true
	}

	return "", false
}

// writeRestoreErr maps a RestoreObject driver error to its S3 response.
func writeRestoreErr(w http.ResponseWriter, err error, versionID string) {
	switch {
	case writeDeleteMarker(w, err, versionID):
	case errors.Is(err, driver.ErrRestoreAlreadyInProgress):
		writeError(w, http.StatusConflict, "RestoreAlreadyInProgress", "Object restore is already in progress")
	case errors.Is(err, driver.ErrObjectAlreadyInActiveTier):
		writeError(w, http.StatusForbidden, "ObjectAlreadyInActiveTierError", "This action is not allowed against this storage tier.")
	case errors.Is(err, driver.ErrRestoreDaysRequired):
		writeError(w, http.StatusBadRequest, "MalformedXML", msgMalformedXML)
	default:
		writeErr(w, err)
	}
}

// writeInvalidObjectState answers an archived-object error with 403
// InvalidObjectState naming the storage class. It returns false when err is
// some other error.
func writeInvalidObjectState(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, driver.ErrInvalidObjectState) {
		return false
	}

	doc := invalidObjectStateXML{Code: "InvalidObjectState", Message: driver.ErrInvalidObjectState.Message}

	var se *driver.InvalidObjectStateError
	if errors.As(err, &se) {
		doc.StorageClass = se.StorageClass
	}

	wire.WriteXML(w, http.StatusForbidden, doc)

	return true
}

// restoreHeader renders the x-amz-restore response header, or "" when the
// object has no active restore.
func restoreHeader(st *driver.ObjectRestoreStatus) string {
	switch {
	case st == nil:
		return ""
	case st.InProgress:
		return `ongoing-request="true"`
	default:
		return fmt.Sprintf(`ongoing-request="false", expiry-date=%q`, st.ExpiryDate.UTC().Format(http.TimeFormat))
	}
}

// wantsRestoreStatus reports whether a listing asked for RestoreStatus via
// x-amz-optional-object-attributes.
func wantsRestoreStatus(h http.Header) bool {
	for _, v := range h.Values("X-Amz-Optional-Object-Attributes") {
		for _, attr := range strings.Split(v, ",") {
			if strings.TrimSpace(attr) == "RestoreStatus" {
				return true
			}
		}
	}

	return false
}

// listRestoreStatus is the RestoreStatus element of a listed object, nil when
// it was not requested or the object has no active restore.
func listRestoreStatus(want bool, st *driver.ObjectRestoreStatus) *restoreStatusXML {
	if !want || st == nil {
		return nil
	}

	out := &restoreStatusXML{IsRestoreInProgress: st.InProgress}
	if !st.InProgress {
		out.RestoreExpiryDate = st.ExpiryDate.UTC().Format(retainUntilLayout)
	}

	return out
}

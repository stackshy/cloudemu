package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// writeObjectLockErr maps a retention/legal-hold driver error: a bucket that was
// not created with Object Lock enabled surfaces as 400 InvalidRequest (real S3);
// everything else (a WORM protection denial → 403 AccessDenied, missing
// bucket/key → NoSuchBucket/NoSuchKey) follows the standard mapping.
func writeObjectLockErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) {
		writeError(w, http.StatusBadRequest, "InvalidRequest", cerrors.Message(err))
		return
	}

	writeErr(w, err)
}

// Object Lock request/response headers and status values.
const (
	hdrObjectLockMode       = "X-Amz-Object-Lock-Mode"
	hdrObjectLockRetainDate = "X-Amz-Object-Lock-Retain-Until-Date"
	hdrObjectLockLegalHold  = "X-Amz-Object-Lock-Legal-Hold"

	legalHoldOn  = "ON"
	legalHoldOff = "OFF"

	// retainUntilLayout is the ISO8601 form S3 emits for a retain-until date
	// (millisecond precision, UTC).
	retainUntilLayout = "2006-01-02T15:04:05.000Z"
)

// objectLockRetentionXML is the Retention document (PUT/GET ?retention).
type objectLockRetentionXML struct {
	XMLName         xml.Name `xml:"Retention"`
	Xmlns           string   `xml:"xmlns,attr,omitempty"`
	Mode            string   `xml:"Mode,omitempty"`
	RetainUntilDate string   `xml:"RetainUntilDate,omitempty"`
}

// objectLockLegalHoldXML is the LegalHold document (PUT/GET ?legal-hold).
type objectLockLegalHoldXML struct {
	XMLName xml.Name `xml:"LegalHold"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	Status  string   `xml:"Status,omitempty"`
}

// parseRetainUntil parses the retain-until-date sent by clients, accepting the
// RFC3339 forms the SDK emits and S3's millisecond ISO8601 form.
func parseRetainUntil(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, retainUntilLayout} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}

	return time.Time{}, false
}

// putRetention answers PUT /{bucket}/{key}?retention. Without an
// Object-Lock-capable driver it is a no-op accept, so a write never falls
// through to overwrite the object.
func (h *Handler) putRetention(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if h.objectLock == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.putObjectRetention(w, r, bucket, key)
}

// getRetention answers the other methods on ?retention. Without an
// Object-Lock-capable driver a GET reports no configuration.
func (h *Handler) getRetention(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if h.objectLock == nil {
		writeNoObjectLock(w, r)
		return
	}

	h.getObjectRetention(w, r, bucket, key)
}

// writeNoObjectLock answers a ?retention or ?legal-hold read on a driver
// without Object Lock: 404 for a GET, an empty 200 otherwise.
func writeNoObjectLock(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeError(w, http.StatusNotFound, "NoSuchObjectLockConfiguration",
			"The specified object does not have an ObjectLock configuration")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) putObjectRetention(w http.ResponseWriter, r *http.Request, bucket, key string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not read request body")
		return
	}

	var doc objectLockRetentionXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "malformed Retention request")
		return
	}

	ret := driver.ObjectRetention{Mode: doc.Mode}

	if doc.RetainUntilDate != "" {
		until, ok := parseRetainUntil(doc.RetainUntilDate)
		if !ok {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid RetainUntilDate")
			return
		}

		ret.RetainUntilDate = until
	}

	versionID := r.URL.Query().Get("versionId")
	if err := h.objectLock.PutObjectRetention(r.Context(), bucket, key, versionID, ret, bypassGovernance(r)); err != nil {
		writeObjectLockErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObjectRetention(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	ret, err := h.objectLock.GetObjectRetention(r.Context(), bucket, key, versionID)
	if err != nil {
		writeErr(w, err)
		return
	}

	if ret.Mode == "" {
		writeError(w, http.StatusNotFound, "NoSuchObjectLockConfiguration",
			"The specified object does not have a Retention configuration")
		return
	}

	wire.WriteXML(w, http.StatusOK, objectLockRetentionXML{
		Xmlns:           xmlns,
		Mode:            ret.Mode,
		RetainUntilDate: ret.RetainUntilDate.UTC().Format(retainUntilLayout),
	})
}

// putLegalHold answers PUT /{bucket}/{key}?legal-hold, a no-op accept
// without an Object-Lock-capable driver.
func (h *Handler) putLegalHold(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if h.objectLock == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.putObjectLegalHold(w, r, bucket, key)
}

// getLegalHold answers the other methods on ?legal-hold.
func (h *Handler) getLegalHold(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if h.objectLock == nil {
		writeNoObjectLock(w, r)
		return
	}

	h.getObjectLegalHold(w, r, bucket, key)
}

func (h *Handler) putObjectLegalHold(w http.ResponseWriter, r *http.Request, bucket, key string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not read request body")
		return
	}

	var doc objectLockLegalHoldXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "malformed LegalHold request")
		return
	}

	versionID := r.URL.Query().Get("versionId")
	on := strings.EqualFold(doc.Status, legalHoldOn)

	if err := h.objectLock.PutObjectLegalHold(r.Context(), bucket, key, versionID, on); err != nil {
		writeObjectLockErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObjectLegalHold(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	on, err := h.objectLock.GetObjectLegalHold(r.Context(), bucket, key, versionID)
	if err != nil {
		writeErr(w, err)
		return
	}

	status := legalHoldOff
	if on {
		status = legalHoldOn
	}

	wire.WriteXML(w, http.StatusOK, objectLockLegalHoldXML{Xmlns: xmlns, Status: status})
}

// parsePutObjectLock reads the Object Lock headers of an upload
// (x-amz-object-lock-mode / -retain-until-date / -legal-hold). It returns nil
// when the request sets none or the driver has no Object Lock. A mode without
// a retain-until date (or the reverse) or an unparseable date is 400
// InvalidArgument; it returns false after writing that error.
func (h *Handler) parsePutObjectLock(w http.ResponseWriter, r *http.Request) (*driver.ObjectLockSettings, bool) {
	if h.objectLock == nil {
		return nil, true
	}

	mode := r.Header.Get(hdrObjectLockMode)
	retainStr := r.Header.Get(hdrObjectLockRetainDate)
	legalHold := r.Header.Get(hdrObjectLockLegalHold)

	if mode == "" && retainStr == "" && legalHold == "" {
		return nil, true
	}

	if (mode == "") != (retainStr == "") {
		writeError(w, http.StatusBadRequest, "InvalidArgument",
			"x-amz-object-lock-retain-until-date and x-amz-object-lock-mode must both be supplied")

		return nil, false
	}

	lock := &driver.ObjectLockSettings{Mode: mode, LegalHold: strings.EqualFold(legalHold, legalHoldOn)}

	if retainStr != "" {
		until, ok := parseRetainUntil(retainStr)
		if !ok {
			writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid x-amz-object-lock-retain-until-date")
			return nil, false
		}

		lock.RetainUntil = until
	}

	return lock, true
}

// applyLockAfterWrite stamps lock onto the version a conditional PutObject just
// wrote. The conditional write path has no lock parameter, so the settings go
// on in a second step there. It returns false after writing the error.
func (h *Handler) applyLockAfterWrite(
	w http.ResponseWriter, r *http.Request, bucket, key, versionID string, lock *driver.ObjectLockSettings,
) bool {
	if lock == nil {
		return true
	}

	if lock.Mode != "" {
		ret := driver.ObjectRetention{Mode: lock.Mode, RetainUntilDate: lock.RetainUntil}
		if err := h.objectLock.PutObjectRetention(r.Context(), bucket, key, versionID, ret, bypassGovernance(r)); err != nil {
			writeObjectLockErr(w, err)
			return false
		}
	}

	if err := h.objectLock.PutObjectLegalHold(r.Context(), bucket, key, versionID, lock.LegalHold); err != nil {
		writeObjectLockErr(w, err)
		return false
	}

	return true
}

// writeObjectLockHeaders echoes an object version's Object Lock state on
// GET/HEAD (x-amz-object-lock-mode / -retain-until-date / -legal-hold). It is
// best-effort: a read error or absent lock omits the headers.
func (h *Handler) writeObjectLockHeaders(w http.ResponseWriter, r *http.Request, bucket, key, versionID string) {
	if h.objectLock == nil {
		return
	}

	if ret, err := h.objectLock.GetObjectRetention(r.Context(), bucket, key, versionID); err == nil && ret.Mode != "" {
		w.Header().Set(hdrObjectLockMode, ret.Mode)
		w.Header().Set(hdrObjectLockRetainDate, ret.RetainUntilDate.UTC().Format(retainUntilLayout))
	}

	if on, err := h.objectLock.GetObjectLegalHold(r.Context(), bucket, key, versionID); err == nil && on {
		w.Header().Set(hdrObjectLockLegalHold, legalHoldOn)
	}
}

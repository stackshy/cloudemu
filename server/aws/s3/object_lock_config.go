package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// subObjectLock is the ?object-lock sub-resource key
// (Put/GetObjectLockConfiguration).
const subObjectLock = "object-lock"

// objectLockEnabledValue is the only valid ObjectLockEnabled value.
const objectLockEnabledValue = "Enabled"

// objectLockConfigXML is the ObjectLockConfiguration document.
type objectLockConfigXML struct {
	XMLName           xml.Name           `xml:"ObjectLockConfiguration"`
	Xmlns             string             `xml:"xmlns,attr,omitempty"`
	ObjectLockEnabled string             `xml:"ObjectLockEnabled,omitempty"`
	Rule              *objectLockRuleXML `xml:"Rule"`
}

type objectLockRuleXML struct {
	DefaultRetention *defaultRetentionXML `xml:"DefaultRetention"`
}

type defaultRetentionXML struct {
	Mode  string `xml:"Mode,omitempty"`
	Days  *int   `xml:"Days"`
	Years *int   `xml:"Years"`
}

// putObjectLockConfiguration answers PUT /{bucket}?object-lock: it enables
// Object Lock on the bucket and sets or clears the default retention.
func (h *Handler) putObjectLockConfiguration(w http.ResponseWriter, r *http.Request, bucket string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", msgMalformedXML)
		return
	}

	cfg, ok := parseObjectLockConfig(body)
	if !ok {
		writeError(w, http.StatusBadRequest, "MalformedXML", msgMalformedXML)
		return
	}

	if err := h.objectLock.PutObjectLockConfiguration(r.Context(), bucket, cfg); err != nil {
		if errors.Is(err, driver.ErrObjectLockNeedsVersioning) {
			writeError(w, http.StatusConflict, "InvalidBucketState", driver.ErrObjectLockNeedsVersioning.Message)
			return
		}

		writeErr(w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// parseObjectLockConfig validates an ObjectLockConfiguration document the way
// S3's schema does: ObjectLockEnabled must be Enabled, and a Rule must hold a
// DefaultRetention with a valid Mode and exactly one of Days or Years. ok is
// false for any violation (S3 answers MalformedXML).
func parseObjectLockConfig(body []byte) (driver.ObjectLockConfiguration, bool) {
	var doc objectLockConfigXML
	if err := xml.Unmarshal(body, &doc); err != nil || doc.ObjectLockEnabled != objectLockEnabledValue {
		return driver.ObjectLockConfiguration{}, false
	}

	if doc.Rule == nil {
		return driver.ObjectLockConfiguration{}, true
	}

	dr := doc.Rule.DefaultRetention
	if dr == nil || (dr.Days == nil) == (dr.Years == nil) ||
		(dr.Mode != driver.ObjectLockGovernance && dr.Mode != driver.ObjectLockCompliance) {
		return driver.ObjectLockConfiguration{}, false
	}

	// A zero or negative period passes the schema; the provider rejects it.
	cfg := driver.ObjectLockConfiguration{DefaultMode: dr.Mode}
	if dr.Days != nil {
		cfg.DefaultDays = *dr.Days
	} else {
		cfg.DefaultYears = *dr.Years
	}

	return cfg, true
}

// getObjectLockConfiguration answers GET /{bucket}?object-lock.
func (h *Handler) getObjectLockConfiguration(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := h.objectLock.GetObjectLockConfiguration(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, driver.ErrNoObjectLockConfiguration) {
			writeError(w, http.StatusNotFound, "ObjectLockConfigurationNotFoundError", driver.ErrNoObjectLockConfiguration.Message)
			return
		}

		writeErr(w, err)

		return
	}

	doc := objectLockConfigXML{Xmlns: xmlns, ObjectLockEnabled: objectLockEnabledValue}

	if cfg.DefaultMode != "" {
		dr := &defaultRetentionXML{Mode: cfg.DefaultMode}
		if cfg.DefaultYears > 0 {
			dr.Years = &cfg.DefaultYears
		} else {
			dr.Days = &cfg.DefaultDays
		}

		doc.Rule = &objectLockRuleXML{DefaultRetention: dr}
	}

	wire.WriteXML(w, http.StatusOK, doc)
}

package azurearm

import (
	"encoding/xml"
	"net/http"
	"net/url"
)

// storageAnalyticsVersion is the version Storage Analytics reports for the
// logging and metrics settings of a new account.
const storageAnalyticsVersion = "1.0"

// compProperties is the comp= value of the service and account property calls.
const compProperties = "properties"

// StorageRetentionPolicy is the RetentionPolicy / DeleteRetentionPolicy
// element of the Storage service properties document.
type StorageRetentionPolicy struct {
	Enabled bool `xml:"Enabled"`
	Days    int  `xml:"Days,omitempty"`
}

// StorageLogging is the Logging element of the service properties document.
type StorageLogging struct {
	Version         string                 `xml:"Version"`
	Delete          bool                   `xml:"Delete"`
	Read            bool                   `xml:"Read"`
	Write           bool                   `xml:"Write"`
	RetentionPolicy StorageRetentionPolicy `xml:"RetentionPolicy"`
}

// StorageMetrics is the HourMetrics / MinuteMetrics element.
type StorageMetrics struct {
	Version         string                 `xml:"Version"`
	Enabled         bool                   `xml:"Enabled"`
	IncludeAPIs     *bool                  `xml:"IncludeAPIs,omitempty"`
	RetentionPolicy StorageRetentionPolicy `xml:"RetentionPolicy"`
}

// StorageCorsRule is one CorsRule element. List-valued fields are
// comma-separated, as on the wire.
type StorageCorsRule struct {
	AllowedOrigins  string `xml:"AllowedOrigins"`
	AllowedMethods  string `xml:"AllowedMethods"`
	AllowedHeaders  string `xml:"AllowedHeaders"`
	ExposedHeaders  string `xml:"ExposedHeaders"`
	MaxAgeInSeconds int    `xml:"MaxAgeInSeconds"`
}

// StorageCors is the Cors element.
type StorageCors struct {
	Rules []StorageCorsRule `xml:"CorsRule"`
}

// StorageServiceProperties is the StorageServiceProperties document of Get
// and Set Service Properties for the Blob, Queue and Table services. Optional
// elements are pointers so a Set request can tell an omitted element (keep the
// current value) from one that is present.
type StorageServiceProperties struct {
	XMLName               xml.Name                `xml:"StorageServiceProperties"`
	Logging               *StorageLogging         `xml:"Logging,omitempty"`
	HourMetrics           *StorageMetrics         `xml:"HourMetrics,omitempty"`
	MinuteMetrics         *StorageMetrics         `xml:"MinuteMetrics,omitempty"`
	Cors                  *StorageCors            `xml:"Cors,omitempty"`
	DefaultServiceVersion string                  `xml:"DefaultServiceVersion,omitempty"`
	DeleteRetentionPolicy *StorageRetentionPolicy `xml:"DeleteRetentionPolicy,omitempty"`
}

// DefaultStorageServiceProperties returns the service properties real Azure
// reports for a new account: logging and metrics off, no CORS rules.
func DefaultStorageServiceProperties() StorageServiceProperties {
	includeAPIs := false

	return StorageServiceProperties{
		Logging:       &StorageLogging{Version: storageAnalyticsVersion},
		HourMetrics:   &StorageMetrics{Version: storageAnalyticsVersion, IncludeAPIs: &includeAPIs},
		MinuteMetrics: &StorageMetrics{Version: storageAnalyticsVersion, IncludeAPIs: &includeAPIs},
		Cors:          &StorageCors{},
	}
}

// DecodeStorageServiceProperties reads a Set Service Properties body, writing
// a 400 InvalidXmlDocument and returning false when it does not parse.
func DecodeStorageServiceProperties(w http.ResponseWriter, r *http.Request) (StorageServiceProperties, bool) {
	var props StorageServiceProperties
	if err := xml.NewDecoder(r.Body).Decode(&props); err != nil {
		writeStorageXMLError(w, http.StatusBadRequest, "InvalidXmlDocument",
			"XML specified is not syntactically valid.")

		return props, false
	}

	return props, true
}

// WriteStorageServiceProperties writes a 200 Get Service Properties response.
func WriteStorageServiceProperties(w http.ResponseWriter, props *StorageServiceProperties) {
	out, err := xml.Marshal(props)
	if err != nil {
		writeStorageXMLError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

// WriteStorageAccountInfo answers Get Account Information
// (?restype=account&comp=properties) with the SKU and kind of a default
// StorageV2 account.
func WriteStorageAccountInfo(w http.ResponseWriter) {
	w.Header().Set("x-ms-sku-name", "Standard_LRS")
	w.Header().Set("x-ms-account-kind", "StorageV2")
	w.Header().Set("x-ms-is-hns-enabled", "false")
	w.WriteHeader(http.StatusOK)
}

// storageXMLError is the Storage service error body.
type storageXMLError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func writeStorageXMLError(w http.ResponseWriter, status int, code, msg string) {
	out, _ := xml.Marshal(storageXMLError{Code: code, Message: msg})

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-ms-error-code", code)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

// ServeStorageServiceOp answers the account-level service operations that
// keep no state in cloudemu's Queue and Table services: Get Service
// Properties reports the defaults, Set Service Properties validates the body
// and returns 202, and Get Account Information reports the account's SKU and
// kind. Any other selector is a 400.
func ServeStorageServiceOp(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if q.Get("comp") != compProperties {
		writeStorageXMLError(w, http.StatusBadRequest, "InvalidQueryParameterValue",
			"Value for one of the query parameters specified in the request URI is invalid.")

		return
	}

	switch op := q.Get("restype") + " " + r.Method; op {
	case restypeAccount + " " + http.MethodGet, restypeAccount + " " + http.MethodHead:
		WriteStorageAccountInfo(w)
	case restypeService + " " + http.MethodGet:
		props := DefaultStorageServiceProperties()
		WriteStorageServiceProperties(w, &props)
	case restypeService + " " + http.MethodPut:
		if _, ok := DecodeStorageServiceProperties(w, r); ok {
			w.WriteHeader(http.StatusAccepted)
		}
	default:
		writeStorageXMLError(w, http.StatusMethodNotAllowed, "UnsupportedHttpVerb",
			"The resource doesn't support specified Http Verb.")
	}
}

// IsStorageServicePropertiesOp reports whether q selects Get or Set Service
// Properties.
func IsStorageServicePropertiesOp(q url.Values) bool {
	return q.Get("restype") == restypeService && q.Get("comp") == compProperties
}

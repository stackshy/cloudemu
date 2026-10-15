package tablestorage

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const compACL = "acl"

// signedIdentifiersXML is the Get/Set Table ACL body.
type signedIdentifiersXML struct {
	XMLName     xml.Name              `xml:"SignedIdentifiers"`
	Identifiers []signedIdentifierXML `xml:"SignedIdentifier"`
}

type signedIdentifierXML struct {
	ID           string `xml:"Id"`
	AccessPolicy struct {
		Start      string `xml:"Start,omitempty"`
		Expiry     string `xml:"Expiry,omitempty"`
		Permission string `xml:"Permission,omitempty"`
	} `xml:"AccessPolicy"`
}

// isTableACL reports whether a request below the account is Get or Set Table
// ACL: a single /{table} segment with comp=acl.
func isTableACL(r *http.Request, path string) bool {
	return r.URL.Query().Get("comp") == compACL && path != "" && !strings.ContainsAny(path, "/(")
}

// tableACL serves GET|HEAD|PUT /{table}?comp=acl. The policies live in the
// account's settings, shared with the ARM table resource's signedIdentifiers.
func (h *Handler) tableACL(w http.ResponseWriter, r *http.Request, account, table string) {
	settings, ok := h.accounts.(storagedriver.AccountServiceSettings)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "table ACLs are not supported")
		return
	}

	if !h.tableExists(r, tableKey(account, table)) {
		writeError(w, http.StatusNotFound, "TableNotFound", "The table specified does not exist.")
		return
	}

	if account == "" {
		account = storagedriver.AzureDefaultStorageAccount
	}

	kind := storagedriver.AzureTableACLKind(table)

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		var ids []storagedriver.SignedIdentifier

		if stored, found, err := settings.AccountSetting(r.Context(), account, kind); err == nil && found {
			_ = json.Unmarshal(stored.Properties, &ids)
		}

		writeACL(w, ids)
	case http.MethodPut:
		putTableACL(w, r, settings, account, kind)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func putTableACL(w http.ResponseWriter, r *http.Request,
	settings storagedriver.AccountServiceSettings, account, kind string,
) {
	var in signedIdentifiersXML
	if err := xml.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidXmlDocument", "XML specified is not syntactically valid.")
		return
	}

	ids := make([]storagedriver.SignedIdentifier, 0, len(in.Identifiers))
	for _, s := range in.Identifiers {
		ids = append(ids, storagedriver.SignedIdentifier{
			ID: s.ID, Start: s.AccessPolicy.Start, Expiry: s.AccessPolicy.Expiry, Permission: s.AccessPolicy.Permission,
		})
	}

	raw, _ := json.Marshal(ids)
	if _, err := settings.SetAccountSetting(r.Context(), account, kind, raw); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// dropTableACL removes a deleted table's stored access policies so a table
// created later under the same name starts without them.
func (h *Handler) dropTableACL(r *http.Request, key string) {
	settings, ok := h.accounts.(storagedriver.AccountServiceSettings)
	if !ok {
		return
	}

	account, table := storagedriver.SplitAzureContainerKey(key)
	if account == "" {
		account = storagedriver.AzureDefaultStorageAccount
	}

	_, _ = settings.DeleteAccountSetting(r.Context(), account, storagedriver.AzureTableACLKind(table))
}

func writeACL(w http.ResponseWriter, ids []storagedriver.SignedIdentifier) {
	out := signedIdentifiersXML{Identifiers: make([]signedIdentifierXML, 0, len(ids))}

	for _, id := range ids {
		s := signedIdentifierXML{ID: id.ID}
		s.AccessPolicy.Start = id.Start
		s.AccessPolicy.Expiry = id.Expiry
		s.AccessPolicy.Permission = id.Permission
		out.Identifiers = append(out.Identifiers, s)
	}

	body, _ := xml.Marshal(out)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

func (h *Handler) tableExists(r *http.Request, key string) bool {
	names, err := h.ts.ListTables(r.Context())
	if err != nil {
		return false
	}

	for _, n := range names {
		if n == key {
			return true
		}
	}

	return false
}

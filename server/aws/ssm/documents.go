package ssm

import (
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ssmnative "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// MaxResults ceilings for the paginated document operations.
const (
	maxResultsDocuments   = 50
	maxResultsVersions    = 50
	maxResultsPermissions = 200
)

// documentOps is the Documents family.
func documentOps() map[string]handlerFunc {
	return map[string]handlerFunc{
		"CreateDocument":               (*Handler).createDocument,
		"GetDocument":                  (*Handler).getDocument,
		"DescribeDocument":             (*Handler).describeDocument,
		"UpdateDocument":               (*Handler).updateDocument,
		"DeleteDocument":               (*Handler).deleteDocument,
		"ListDocuments":                (*Handler).listDocuments,
		"ListDocumentVersions":         (*Handler).listDocumentVersions,
		"UpdateDocumentDefaultVersion": (*Handler).updateDocumentDefaultVersion,
		"DescribeDocumentPermission":   (*Handler).describeDocumentPermission,
		"ModifyDocumentPermission":     (*Handler).modifyDocumentPermission,
	}
}

type documentParameterJSON struct {
	Name         string `json:"Name"`
	Type         string `json:"Type,omitempty"`
	Description  string `json:"Description,omitempty"`
	DefaultValue string `json:"DefaultValue,omitempty"`
}

type documentDescriptionJSON struct {
	Name            string                  `json:"Name"`
	DisplayName     string                  `json:"DisplayName,omitempty"`
	VersionName     string                  `json:"VersionName,omitempty"`
	Owner           string                  `json:"Owner"`
	CreatedDate     float64                 `json:"CreatedDate"`
	Status          string                  `json:"Status"`
	DocumentVersion string                  `json:"DocumentVersion"`
	Description     string                  `json:"Description,omitempty"`
	Parameters      []documentParameterJSON `json:"Parameters,omitempty"`
	PlatformTypes   []string                `json:"PlatformTypes"`
	DocumentType    string                  `json:"DocumentType"`
	SchemaVersion   string                  `json:"SchemaVersion,omitempty"`
	LatestVersion   string                  `json:"LatestVersion"`
	DefaultVersion  string                  `json:"DefaultVersion"`
	DocumentFormat  string                  `json:"DocumentFormat"`
	TargetType      string                  `json:"TargetType,omitempty"`
	Hash            string                  `json:"Hash"`
	HashType        string                  `json:"HashType"`
	Tags            []ssmTag                `json:"Tags"`
}

type documentIdentifierJSON struct {
	Name            string   `json:"Name"`
	CreatedDate     float64  `json:"CreatedDate"`
	DisplayName     string   `json:"DisplayName,omitempty"`
	Owner           string   `json:"Owner"`
	VersionName     string   `json:"VersionName,omitempty"`
	PlatformTypes   []string `json:"PlatformTypes"`
	DocumentVersion string   `json:"DocumentVersion"`
	DocumentType    string   `json:"DocumentType"`
	SchemaVersion   string   `json:"SchemaVersion,omitempty"`
	DocumentFormat  string   `json:"DocumentFormat"`
	TargetType      string   `json:"TargetType,omitempty"`
	Tags            []ssmTag `json:"Tags"`
}

func epoch(t time.Time) float64 {
	return float64(t.UnixMilli()) / 1000 //nolint:mnd // milliseconds per second
}

func toDescriptionJSON(d *ssmnative.DocumentDescription) documentDescriptionJSON {
	out := documentDescriptionJSON{
		Name: d.Name, DisplayName: d.DisplayName, VersionName: d.VersionName, Owner: d.Owner,
		CreatedDate: epoch(d.CreatedDate), Status: d.Status, DocumentVersion: d.DocumentVersion,
		Description: d.Description, PlatformTypes: nonNil(d.PlatformTypes), DocumentType: d.DocumentType,
		SchemaVersion: d.SchemaVersion, LatestVersion: d.LatestVersion, DefaultVersion: d.DefaultVersion,
		DocumentFormat: d.DocumentFormat, TargetType: d.TargetType, Hash: d.Hash, HashType: d.HashType,
		Tags: tagList(d.Tags),
	}

	for _, p := range d.Parameters {
		out.Parameters = append(out.Parameters, documentParameterJSON(p))
	}

	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// documents reports whether the driver supports Documents, writing the
// error response when it does not.
func (h *Handler) documents(w http.ResponseWriter) (ssmnative.Documents, bool) {
	d, ok := h.store.(ssmnative.Documents)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnsupportedOperationException", "this driver does not support SSM documents")
	}

	return d, ok
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req struct {
		Name, Content, DisplayName, VersionName, DocumentType, DocumentFormat, TargetType string
		Tags                                                                              []ssmTag
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	var tags map[string]string
	if len(req.Tags) > 0 {
		tags = make(map[string]string, len(req.Tags))
		for _, t := range req.Tags {
			tags[t.Key] = t.Value
		}
	}

	d, err := store.CreateDocument(r.Context(), &ssmnative.CreateDocumentInput{
		Name: req.Name, Content: req.Content, DisplayName: req.DisplayName, VersionName: req.VersionName,
		DocumentType: req.DocumentType, DocumentFormat: req.DocumentFormat, TargetType: req.TargetType, Tags: tags,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"DocumentDescription": toDescriptionJSON(d)})
}

// documentRefRequest is the Name/DocumentVersion/VersionName selector many
// document operations share.
type documentRefRequest struct {
	Name            string
	DocumentVersion string
	VersionName     string
	DocumentFormat  string
}

func (q *documentRefRequest) ref() ssmnative.DocumentRef {
	return ssmnative.DocumentRef{Name: q.Name, Version: q.DocumentVersion, VersionName: q.VersionName}
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req documentRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := store.GetDocument(r.Context(), req.ref(), req.DocumentFormat)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"Name": c.Name, "CreatedDate": epoch(c.CreatedDate), "DisplayName": c.DisplayName,
		"VersionName": c.VersionName, "DocumentVersion": c.DocumentVersion, "Status": c.Status,
		"Content": c.Content, "DocumentType": c.DocumentType, "DocumentFormat": c.DocumentFormat,
	})
}

func (h *Handler) describeDocument(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req documentRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := store.DescribeDocument(r.Context(), req.ref())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"Document": toDescriptionJSON(d)})
}

func (h *Handler) updateDocument(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req struct {
		Name, Content, DisplayName, VersionName, DocumentVersion, DocumentFormat, TargetType string
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := store.UpdateDocument(r.Context(), &ssmnative.UpdateDocumentInput{
		Name: req.Name, Content: req.Content, DisplayName: req.DisplayName, VersionName: req.VersionName,
		Version: req.DocumentVersion, DocumentFormat: req.DocumentFormat, TargetType: req.TargetType,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"DocumentDescription": toDescriptionJSON(d)})
}

func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req documentRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := store.DeleteDocument(r.Context(), req.ref()); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

type listDocumentsRequest struct {
	DocumentFilterList []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	Filters    []ssmnative.DocumentFilter
	MaxResults int32
	NextToken  string
}

// filters merges the legacy DocumentFilterList into the Filters shape. Real
// SSM rejects a request that sends both.
func (q *listDocumentsRequest) filters() ([]ssmnative.DocumentFilter, error) {
	if len(q.DocumentFilterList) > 0 && len(q.Filters) > 0 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"You can specify either DocumentFilterList or Filters, but not both.")
	}

	out := append([]ssmnative.DocumentFilter{}, q.Filters...)
	for _, f := range q.DocumentFilterList {
		out = append(out, ssmnative.DocumentFilter{Key: f.Key, Values: []string{f.Value}})
	}

	return out, nil
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req listDocumentsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	filters, err := req.filters()
	if err != nil {
		writeErr(w, err)
		return
	}

	docs, err := store.ListDocuments(r.Context(), filters)
	if err != nil {
		writeErr(w, err)
		return
	}

	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, maxResultsDocuments, len(docs))
	if err != nil {
		writeErr(w, err)
		return
	}

	ids := make([]documentIdentifierJSON, 0, end-start)

	for i := start; i < end; i++ {
		d := &docs[i]
		ids = append(ids, documentIdentifierJSON{
			Name: d.Name, CreatedDate: epoch(d.CreatedDate), DisplayName: d.DisplayName, Owner: d.Owner,
			VersionName: d.VersionName, PlatformTypes: nonNil(d.PlatformTypes), DocumentVersion: d.DocumentVersion,
			DocumentType: d.DocumentType, SchemaVersion: d.SchemaVersion, DocumentFormat: d.DocumentFormat,
			TargetType: d.TargetType, Tags: tagList(d.Tags),
		})
	}

	wire.WriteJSON(w, map[string]any{"DocumentIdentifiers": ids, "NextToken": omitEmpty(next)})
}

// omitEmpty returns nil for "" so an absent NextToken encodes as null, which
// the SDK reads as no more pages.
func omitEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func (h *Handler) listDocumentVersions(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req struct {
		Name       string
		MaxResults int32
		NextToken  string
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	versions, err := store.ListDocumentVersions(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, maxResultsVersions, len(versions))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]map[string]any, 0, end-start)

	for i := start; i < end; i++ {
		v := &versions[i]
		out = append(out, map[string]any{
			"Name": v.Name, "DisplayName": omitEmpty(v.DisplayName), "DocumentVersion": v.DocumentVersion,
			"VersionName": omitEmpty(v.VersionName), "CreatedDate": epoch(v.CreatedDate),
			"IsDefaultVersion": v.IsDefaultVersion, "DocumentFormat": v.DocumentFormat, "Status": v.Status,
		})
	}

	wire.WriteJSON(w, map[string]any{"DocumentVersions": out, "NextToken": omitEmpty(next)})
}

func (h *Handler) updateDocumentDefaultVersion(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req struct{ Name, DocumentVersion string }

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	res, err := store.UpdateDocumentDefaultVersion(r.Context(), req.Name, req.DocumentVersion)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"Description": map[string]any{
		"Name": res.Name, "DefaultVersion": res.DefaultVersion, "DefaultVersionName": omitEmpty(res.DefaultVersionName),
	}})
}

func (h *Handler) describeDocumentPermission(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req struct {
		Name, PermissionType, NextToken string
		MaxResults                      int32
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	shares, err := store.DescribeDocumentPermission(r.Context(), req.Name, req.PermissionType)
	if err != nil {
		writeErr(w, err)
		return
	}

	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, maxResultsPermissions, len(shares))
	if err != nil {
		writeErr(w, err)
		return
	}

	ids := make([]string, 0, end-start)
	infos := make([]map[string]string, 0, end-start)

	for _, s := range shares[start:end] {
		ids = append(ids, s.AccountID)
		infos = append(infos, map[string]string{"AccountId": s.AccountID, "SharedDocumentVersion": s.SharedDocumentVersion})
	}

	wire.WriteJSON(w, map[string]any{
		"AccountIds": ids, "AccountSharingInfoList": infos, "NextToken": omitEmpty(next),
	})
}

func (h *Handler) modifyDocumentPermission(w http.ResponseWriter, r *http.Request) {
	store, ok := h.documents(w)
	if !ok {
		return
	}

	var req struct {
		Name, PermissionType, SharedDocumentVersion string
		AccountIDsToAdd                             []string `json:"AccountIdsToAdd"`
		AccountIDsToRemove                          []string `json:"AccountIdsToRemove"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	err := store.ModifyDocumentPermission(r.Context(), &ssmnative.ModifyPermissionInput{
		Name: req.Name, PermissionType: req.PermissionType, SharedDocumentVersion: req.SharedDocumentVersion,
		AccountIDsToAdd: req.AccountIDsToAdd, AccountIDsToRemove: req.AccountIDsToRemove,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

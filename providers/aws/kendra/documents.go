package kendra

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Documented BatchPutDocument / BatchDeleteDocument / BatchGetDocumentStatus
// limits.
const (
	maxBatchDocuments  = 10
	maxDocumentIDLen   = 2048
	maxDocumentTitle   = 1024
	maxBlobBytes       = 50 << 20
	maxExtractedBytes  = 5 << 20
	maxAttributesCount = 500
)

// validContentTypes are the document content types BatchPutDocument accepts.
//
//nolint:gochecknoglobals // static validation set
var validContentTypes = map[string]bool{
	"PDF": true, "HTML": true, "MS_WORD": true, "PLAIN_TEXT": true, "PPT": true, "RTF": true,
	"XML": true, "XSLT": true, "MS_EXCEL": true, "CSV": true, "JSON": true, "MD": true,
}

// binaryContentTypes cannot be read as text, so only the title and attributes of
// such a document are searchable.
//
//nolint:gochecknoglobals // static lookup set
var binaryContentTypes = map[string]bool{"PDF": true, "MS_WORD": true, "PPT": true, "MS_EXCEL": true}

var tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// storedDocument is a document held by an index: its extracted text (what
// search runs over), title, attributes and ingestion bookkeeping.
type storedDocument struct {
	IndexID                       string                     `json:"indexId"`
	ID                            string                     `json:"id"`
	Title                         string                     `json:"title,omitempty"`
	ContentType                   string                     `json:"contentType,omitempty"`
	Text                          string                     `json:"text,omitempty"`
	S3Path                        *driver.S3Path             `json:"s3Path,omitempty"`
	Attributes                    []driver.DocumentAttribute `json:"attributes,omitempty"`
	AccessControlConfigurationID  string                     `json:"accessControlConfigurationId,omitempty"`
	AccessControlList             json.RawMessage            `json:"accessControlList,omitempty"`
	HierarchicalAccessControlList json.RawMessage            `json:"hierarchicalAccessControlList,omitempty"`
	CreatedAt                     time.Time                  `json:"createdAt"`
	UpdatedAt                     time.Time                  `json:"updatedAt"`
	Replaced                      bool                       `json:"replaced,omitempty"`
}

func documentKey(indexID, id string) string { return indexID + "/" + id }

// extractText returns the searchable text of a document body. Text formats are
// read as UTF-8 (HTML has its tags removed); binary formats yield no text.
func extractText(contentType string, blob []byte) string {
	if len(blob) == 0 || binaryContentTypes[contentType] || !utf8.Valid(blob) {
		return ""
	}

	text := string(blob)
	if contentType == "HTML" || contentType == "XML" || contentType == "XSLT" {
		text = html.UnescapeString(tagPattern.ReplaceAllString(text, " "))
	}

	return text
}

// validatePutDocuments applies the request-level constraints of BatchPutDocument.
func validatePutDocuments(in *driver.PutDocumentsInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	if n := len(in.Documents); n < 1 || n > maxBatchDocuments {
		return validation("Documents must have between 1 and %d items", maxBatchDocuments)
	}

	if in.RoleArn != "" {
		if err := validateRoleArn(in.RoleArn); err != nil {
			return err
		}
	}

	for i := range in.Documents {
		if err := validatePutDocument(&in.Documents[i]); err != nil {
			return err
		}
	}

	return nil
}

// validatePutDocument applies the per-document constraints of BatchPutDocument.
func validatePutDocument(d *driver.PutDocument) error {
	switch {
	case len(d.ID) < 1 || len(d.ID) > maxDocumentIDLen:
		return validation("document Id must have length between 1 and %d", maxDocumentIDLen)
	case len(d.Title) > maxDocumentTitle:
		return validation("document Title must have length between 1 and %d", maxDocumentTitle)
	case d.ContentType != "" && !validContentTypes[d.ContentType]:
		return validation("invalid ContentType: %q", d.ContentType)
	case len(d.Attributes) > maxAttributesCount:
		return validation("document Attributes must have at most %d items", maxAttributesCount)
	}

	return nil
}

// BatchPutDocument adds documents to an index. Documents are indexed
// synchronously (PROCESSING first under async settling). A document the service
// cannot ingest comes back in the returned failures with an InvalidRequest code
// instead of failing the whole batch. Putting an existing id replaces it.
func (m *Mock) BatchPutDocument(_ context.Context, in *driver.PutDocumentsInput) ([]driver.FailedDocument, error) {
	if err := validatePutDocuments(in); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	failed := []driver.FailedDocument{}
	now := m.now()

	for i := range in.Documents {
		d := &in.Documents[i]

		if len(d.Blob) > maxBlobBytes {
			failed = append(failed, failedDoc(d.ID, "the document is larger than the 50 MB limit"))

			continue
		}

		if len(d.Blob) == 0 && d.S3Path == nil {
			failed = append(failed, failedDoc(d.ID, "a document needs either Blob or S3Path"))

			continue
		}

		text := extractText(d.ContentType, d.Blob)
		if len(text) > maxExtractedBytes {
			failed = append(failed, failedDoc(d.ID, "the document has more than 5 MB of extracted text"))

			continue
		}

		key := documentKey(in.IndexID, d.ID)
		existing, replaced := m.documents.Get(key)
		created := now

		if replaced {
			created = existing.CreatedAt
		}

		m.documents.Set(key, storedDocument{
			IndexID: in.IndexID, ID: d.ID, Title: d.Title, ContentType: d.ContentType, Text: text,
			S3Path:                        copyS3Path(d.S3Path),
			Attributes:                    copyAttributes(d.Attributes),
			AccessControlConfigurationID:  d.AccessControlConfigurationID,
			AccessControlList:             copyRaw(d.AccessControlList),
			HierarchicalAccessControlList: copyRaw(d.HierarchicalAccessControlList),
			CreatedAt:                     created, UpdatedAt: now, Replaced: replaced,
		})
		m.beginSettle(docSettleKey(key), driver.DocStatusProcessing)
	}

	m.recordIndexedMetrics(in.IndexID, len(in.Documents)-len(failed), len(failed))

	return failed, nil
}

func failedDoc(id, msg string) driver.FailedDocument {
	return driver.FailedDocument{ID: id, ErrorCode: driver.ErrCodeInvalidRequest, ErrorMessage: msg}
}

// BatchDeleteDocument removes documents from an index. Ids that are not in the
// index are ignored, as in the real service, and a later BatchGetDocumentStatus
// reports them NOT_FOUND.
func (m *Mock) BatchDeleteDocument(_ context.Context, in *driver.DeleteDocumentsInput) ([]driver.FailedDocument, error) {
	if err := validateDocumentIDs("DocumentIdList", in.IndexID, in.DocumentIDs); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	if t := in.MetricTarget; t != nil {
		if _, err := m.getDataSource(in.IndexID, t.DataSourceID); err != nil {
			return nil, err
		}
	}

	deleted := 0

	for _, id := range in.DocumentIDs {
		key := documentKey(in.IndexID, id)
		if m.documents.Delete(key) {
			deleted++

			m.settling.Clear(docSettleKey(key))
		}
	}

	if t := in.MetricTarget; t != nil {
		m.addDeletedMetric(in.IndexID, t, deleted)
	}

	return []driver.FailedDocument{}, nil
}

// validateDocumentIDs checks the index id and a 1..10 list of document ids.
func validateDocumentIDs(field, indexID string, ids []string) error {
	if err := validateIndexID(indexID); err != nil {
		return err
	}

	if n := len(ids); n < 1 || n > maxBatchDocuments {
		return validation("%s must have between 1 and %d items", field, maxBatchDocuments)
	}

	for _, id := range ids {
		if len(id) < 1 || len(id) > maxDocumentIDLen {
			return validation("document id must have length between 1 and %d", maxDocumentIDLen)
		}
	}

	return nil
}

// BatchGetDocumentStatus reports the indexing status of documents. A document
// that is not in the index (never added, or deleted) is NOT_FOUND.
func (m *Mock) BatchGetDocumentStatus(
	_ context.Context, indexID string, docs []driver.DocumentInfo,
) (*driver.DocumentStatusResult, error) {
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = docs[i].DocumentID
	}

	if err := validateDocumentIDs("DocumentInfoList", indexID, ids); err != nil {
		return nil, err
	}

	if _, err := m.getIndex(indexID); err != nil {
		return nil, err
	}

	out := &driver.DocumentStatusResult{
		Statuses: make([]driver.DocumentStatus, 0, len(ids)),
		Errors:   []driver.DocumentStatusError{},
	}

	for _, id := range ids {
		out.Statuses = append(out.Statuses, driver.DocumentStatus{DocumentID: id, Status: m.documentStatus(indexID, id)})
	}

	return out, nil
}

// documentStatus is a document's indexing status as observed now.
func (m *Mock) documentStatus(indexID, id string) string {
	key := documentKey(indexID, id)

	doc, ok := m.documents.Get(key)
	if !ok {
		return driver.DocStatusNotFound
	}

	final := driver.DocStatusIndexed
	if doc.Replaced {
		final = driver.DocStatusUpdated
	}

	return m.settleStatus(docSettleKey(key), final)
}

// docSettleKey namespaces a document's settle window so a document id equal to a
// data source, child or "suggestions" id cannot touch that resource's window.
func docSettleKey(key string) string { return "document:" + key }

func copyS3Path(p *driver.S3Path) *driver.S3Path {
	if p == nil {
		return nil
	}

	c := *p

	return &c
}

func copyAttributes(in []driver.DocumentAttribute) []driver.DocumentAttribute {
	if in == nil {
		return nil
	}

	out := make([]driver.DocumentAttribute, len(in))
	for i := range in {
		out[i] = driver.DocumentAttribute{Key: in[i].Key, Value: copyAttributeValue(in[i].Value)}
	}

	return out
}

func copyAttributeValue(v driver.DocumentAttributeValue) driver.DocumentAttributeValue {
	out := driver.DocumentAttributeValue{}

	if v.StringValue != nil {
		s := *v.StringValue
		out.StringValue = &s
	}

	if v.StringListValue != nil {
		out.StringListValue = append([]string(nil), v.StringListValue...)
	}

	if v.LongValue != nil {
		l := *v.LongValue
		out.LongValue = &l
	}

	if v.DateValue != nil {
		d := *v.DateValue
		out.DateValue = &d
	}

	return out
}

// documentsOf returns the documents of an index ordered by id.
func (m *Mock) documentsOf(indexID string) []storedDocument {
	prefix := indexID + "/"
	all := m.documents.SortedValues()
	out := make([]storedDocument, 0, len(all))

	for i := range all {
		if strings.HasPrefix(documentKey(all[i].IndexID, all[i].ID), prefix) {
			out = append(out, all[i])
		}
	}

	return out
}

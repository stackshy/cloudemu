package bedrockagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Document call bounds from the service model.
const (
	minDocumentsPerCall = 1
	maxDocumentsPerCall = 10
	maxCustomDocIDLen   = 2048
	s3URIPrefix         = "s3://"
	mimeTextPlain       = "text/plain"
)

// storedDocument is a document ingested into a data source. Text is the text
// read from S3 (an S3 document, or one an ingestion job synced: Synced, with
// the object's ETag); CUSTOM documents keep their text in Content.
type storedDocument struct {
	Detail   driver.DocumentDetail `json:"detail"`
	Content  json.RawMessage       `json:"content,omitempty"`
	Metadata json.RawMessage       `json:"metadata,omitempty"`
	Text     string                `json:"text,omitempty"`
	ETag     string                `json:"etag,omitempty"`
	Synced   bool                  `json:"synced,omitempty"`
}

// documentContent is the part of the DocumentContent union the mock reads.
type documentContent struct {
	DataSourceType string `json:"dataSourceType"`
	Custom         *struct {
		CustomDocumentIdentifier *struct {
			ID string `json:"id"`
		} `json:"customDocumentIdentifier"`
		SourceType string `json:"sourceType"`
	} `json:"custom"`
	S3 *struct {
		S3Location *struct {
			URI string `json:"uri"`
		} `json:"s3Location"`
	} `json:"s3"`
}

// IngestKnowledgeBaseDocuments adds or replaces documents of a CUSTOM or S3
// data source; each is INDEXED at once.
func (m *Mock) IngestKnowledgeBaseDocuments(ctx context.Context, kbID, dsID string, docs []driver.KnowledgeBaseDocument) (
	[]driver.DocumentDetail, error,
) {
	if err := checkDocumentCount("documents", len(docs)); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sourceType, err := m.documentSourceType(kbID, dsID)
	if err != nil {
		return nil, err
	}

	ids := make([]driver.DocumentIdentifier, 0, len(docs))

	for _, d := range docs {
		id, err := parseDocumentContent(d.Content, sourceType)
		if err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	now := m.now()
	out := make([]driver.DocumentDetail, 0, len(docs))

	for i, d := range docs {
		detail := driver.DocumentDetail{
			KnowledgeBaseID: kbID, DataSourceID: dsID, Identifier: ids[i],
			Status: driver.DocumentIndexed, UpdatedAt: now,
		}
		m.documents.Set(documentKey(kbID, dsID, ids[i]), &storedDocument{
			Detail: detail, Content: copyRaw(d.Content), Metadata: m.resolveS3Metadata(ctx, copyRaw(d.Metadata)),
			Text: m.s3DocumentText(ctx, ids[i]),
		})

		out = append(out, detail)
	}

	return out, nil
}

// GetKnowledgeBaseDocuments reports the status of each named document;
// documents the data source does not hold are NOT_FOUND.
func (m *Mock) GetKnowledgeBaseDocuments(_ context.Context, kbID, dsID string, ids []driver.DocumentIdentifier) (
	[]driver.DocumentDetail, error,
) {
	if err := m.checkDocumentIDs(kbID, dsID, ids); err != nil {
		return nil, err
	}

	out := make([]driver.DocumentDetail, 0, len(ids))

	for _, id := range ids {
		if doc, ok := m.documents.Get(documentKey(kbID, dsID, id)); ok {
			out = append(out, doc.Detail)

			continue
		}

		out = append(out, driver.DocumentDetail{
			KnowledgeBaseID: kbID, DataSourceID: dsID, Identifier: id, Status: driver.DocumentNotFound,
		})
	}

	return out, nil
}

// ListKnowledgeBaseDocuments lists one page of a data source's documents.
func (m *Mock) ListKnowledgeBaseDocuments(_ context.Context, kbID, dsID string, page driver.Page) (
	[]driver.DocumentDetail, string, error,
) {
	if m.findDataSource(kbID, dsID) == nil {
		return nil, "", errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	items := childrenOf(m.documents, versionKey(kbID, dsID), func(d *storedDocument) driver.DocumentDetail { return d.Detail })

	return paginate(items, page)
}

// DeleteKnowledgeBaseDocuments deletes the named documents; each held one is
// reported DELETING, any other NOT_FOUND.
func (m *Mock) DeleteKnowledgeBaseDocuments(_ context.Context, kbID, dsID string, ids []driver.DocumentIdentifier) (
	[]driver.DocumentDetail, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkDocumentIDs(kbID, dsID, ids); err != nil {
		return nil, err
	}

	now := m.now()
	out := make([]driver.DocumentDetail, 0, len(ids))

	for _, id := range ids {
		detail := driver.DocumentDetail{KnowledgeBaseID: kbID, DataSourceID: dsID, Identifier: id, Status: driver.DocumentNotFound}

		key := documentKey(kbID, dsID, id)
		if m.documents.Has(key) {
			m.documents.Delete(key)

			detail.Status = driver.DocumentDeleting
			detail.UpdatedAt = now
		}

		out = append(out, detail)
	}

	return out, nil
}

// checkDocumentIDs validates identifiers against the data source's type.
func (m *Mock) checkDocumentIDs(kbID, dsID string, ids []driver.DocumentIdentifier) error {
	if err := checkDocumentCount("documentIdentifiers", len(ids)); err != nil {
		return err
	}

	sourceType, err := m.documentSourceType(kbID, dsID)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := validateDocumentIdentifier(id, sourceType); err != nil {
			return err
		}
	}

	return nil
}

// documentSourceType resolves the data source and returns its type, which must
// be CUSTOM or S3 for the documents API.
func (m *Mock) documentSourceType(kbID, dsID string) (string, error) {
	ds := m.findDataSource(kbID, dsID)
	if ds == nil {
		return "", errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	var cfg struct {
		Type string `json:"type"`
	}

	_ = json.Unmarshal(ds.DataSourceConfiguration, &cfg)

	if cfg.Type != driver.DocumentSourceCustom && cfg.Type != driver.DocumentSourceS3 {
		return "", errors.Newf(errors.InvalidArgument,
			"Data source %s is of type %s. Documents can be managed directly only in CUSTOM and S3 data sources.",
			dsID, cfg.Type)
	}

	return cfg.Type, nil
}

// parseDocumentContent reads the identifier out of a DocumentContent union.
func parseDocumentContent(raw json.RawMessage, sourceType string) (driver.DocumentIdentifier, error) {
	var c documentContent
	if err := json.Unmarshal(raw, &c); err != nil {
		return driver.DocumentIdentifier{}, errors.New(errors.InvalidArgument, "documents.member.content is not a valid object")
	}

	id := driver.DocumentIdentifier{DataSourceType: c.DataSourceType}

	if c.Custom != nil && c.Custom.CustomDocumentIdentifier != nil {
		id.CustomID = c.Custom.CustomDocumentIdentifier.ID
	}

	if c.S3 != nil && c.S3.S3Location != nil {
		id.S3URI = c.S3.S3Location.URI
	}

	return id, validateDocumentIdentifier(id, sourceType)
}

// validateDocumentIdentifier checks an identifier names a document of the
// data source's type.
func validateDocumentIdentifier(id driver.DocumentIdentifier, sourceType string) error {
	var v violations

	v.required("dataSourceType", id.DataSourceType == "")
	v.enum("dataSourceType", id.DataSourceType, driver.DocumentSourceCustom, driver.DocumentSourceS3)

	switch id.DataSourceType {
	case driver.DocumentSourceCustom:
		v.required("custom.id", id.CustomID == "")
		v.maxLen("custom.id", id.CustomID, maxCustomDocIDLen)
	case driver.DocumentSourceS3:
		v.required("s3.uri", id.S3URI == "")

		if id.S3URI != "" && (len(id.S3URI) <= len(s3URIPrefix) || id.S3URI[:len(s3URIPrefix)] != s3URIPrefix) {
			v.addf("Value '%s' at 's3.uri' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: s3://.{1,128}", id.S3URI)
		}
	}

	if err := v.err(); err != nil {
		return err
	}

	if id.DataSourceType != sourceType {
		return errors.Newf(errors.InvalidArgument,
			"The document's dataSourceType %s does not match the data source type %s.", id.DataSourceType, sourceType)
	}

	return nil
}

func checkDocumentCount(field string, n int) error {
	var v violations

	if n < minDocumentsPerCall || n > maxDocumentsPerCall {
		v.addf("Value at '%s' failed to satisfy constraint: Member must have length less than or equal to %d, "+
			"Member must have length greater than or equal to %d", field, maxDocumentsPerCall, minDocumentsPerCall)
	}

	return v.err()
}

// documentKey is a document's store key: "<kb>/<ds>/<type>:<id or uri>".
func documentKey(kbID, dsID string, id driver.DocumentIdentifier) string {
	name := id.CustomID
	if id.DataSourceType == driver.DocumentSourceS3 {
		name = id.S3URI
	}

	return versionKey(kbID, dsID, id.DataSourceType+":"+name)
}

// inlineContent is the inline part of a CUSTOM document's content.
type inlineContent struct {
	Custom *struct {
		InlineContent *struct {
			Type        string `json:"type"`
			TextContent *struct {
				Data string `json:"data"`
			} `json:"textContent"`
			ByteContent *struct {
				Data     []byte `json:"data"`
				MimeType string `json:"mimeType"`
			} `json:"byteContent"`
		} `json:"inlineContent"`
	} `json:"custom"`
}

// IndexedDocuments returns every document ingested into a knowledge base,
// sorted by data source and identifier, with the text the runtime searches.
// The bedrock-agent-runtime mock reads it; it is not an AWS operation.
func (m *Mock) IndexedDocuments(kbID string) []driver.IndexedDocument {
	docs := childrenOf(m.documents, kbID, func(d *storedDocument) *storedDocument { return d })
	out := make([]driver.IndexedDocument, 0, len(docs))

	for _, d := range docs {
		text, mime := documentText(d.Content)
		if d.Text != "" {
			text, mime = d.Text, mimeTextPlain
		}

		out = append(out, driver.IndexedDocument{
			KnowledgeBaseID: d.Detail.KnowledgeBaseID,
			DataSourceID:    d.Detail.DataSourceID,
			Identifier:      d.Detail.Identifier,
			Text:            text,
			MimeType:        mime,
			Attributes:      documentAttributes(d.Metadata),
		})
	}

	return out
}

// documentText extracts inline text from a document's content: TEXT inline
// content, or BYTE content whose MIME type is text.
func documentText(raw json.RawMessage) (text, mime string) {
	var c inlineContent
	if json.Unmarshal(raw, &c) != nil || c.Custom == nil || c.Custom.InlineContent == nil {
		return "", ""
	}

	in := c.Custom.InlineContent

	switch {
	case in.TextContent != nil:
		return in.TextContent.Data, mimeTextPlain
	case in.ByteContent != nil && strings.HasPrefix(in.ByteContent.MimeType, "text/"):
		return string(in.ByteContent.Data), in.ByteContent.MimeType
	default:
		return "", ""
	}
}

// s3DocumentText reads the text of an S3 document ingested directly, when the
// object exists in cloudemu S3 and is a text type.
func (m *Mock) s3DocumentText(ctx context.Context, id driver.DocumentIdentifier) string {
	if m.objects == nil || id.DataSourceType != driver.DocumentSourceS3 {
		return ""
	}

	bucket, key, ok := strings.Cut(strings.TrimPrefix(id.S3URI, s3URIPrefix), "/")
	if !ok {
		return ""
	}

	text, _ := m.readText(ctx, bucket, key, "")

	return text
}

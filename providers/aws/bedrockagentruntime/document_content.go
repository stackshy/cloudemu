package bedrockagentruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Document content constants.
const (
	docTokenBytes    = 16
	docTokenLifetime = 15 * time.Minute
	formatRaw        = "RAW"
	formatExtracted  = "EXTRACTED"
)

// docToken is an issued, expiring handle on a document's content.
type docToken struct {
	Text      string `json:"text"`
	MimeType  string `json:"mimeType"`
	ExpiresAt string `json:"expiresAt"`
}

// GetDocumentContent issues a short-lived handle on the text of a document in
// a knowledge base (inline text, or a text object read from S3); the wire
// layer turns it into a URL the emulator serves. Documents with no text
// (binary content) can't be read.
func (m *Mock) GetDocumentContent(ctx context.Context, kbID, dsID, documentID, format string) (*driver.DocumentContent, error) {
	var v violations

	v.required("documentId", documentID == "")
	v.enum("outputFormat", format, formatRaw, formatExtracted)

	if err := v.err(); err != nil {
		return nil, err
	}

	if m.cp == nil {
		return nil, errors.New(errors.InvalidArgument, "document content needs the bedrock-agent control plane")
	}

	kbID = knowledgeBaseID(kbID)

	if _, err := m.cp.GetDataSource(ctx, kbID, dsID); err != nil {
		return nil, err
	}

	doc, ok := findDocument(m.cp.IndexedDocuments(kbID), dsID, documentID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "document %q not found in data source %s", documentID, dsID)
	}

	if doc.Text == "" {
		return nil, errors.Newf(errors.InvalidArgument,
			"The emulator stores no text for document %q (only text documents can be read).", documentID)
	}

	var b [docTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}

	m.sweepDocTokens()

	token := hex.EncodeToString(b[:])
	m.docTokens.Set(token, &docToken{
		Text: doc.Text, MimeType: doc.MimeType, ExpiresAt: m.now().Add(docTokenLifetime).Format(time.RFC3339Nano),
	})

	return &driver.DocumentContent{MimeType: doc.MimeType, Length: int64(len(doc.Text)), Token: token}, nil
}

// ReadDocumentContent returns the content behind an issued handle; an unknown
// or expired handle is PermissionDenied (403, like an expired presigned URL).
func (m *Mock) ReadDocumentContent(_ context.Context, token string) (body []byte, mimeType string, err error) {
	t, ok := m.docTokens.Get(token)
	if !ok {
		return nil, "", errors.New(errors.PermissionDenied, "Request has expired or the signature is invalid")
	}

	if m.expired(t) {
		m.docTokens.Delete(token)

		return nil, "", errors.New(errors.PermissionDenied, "Request has expired")
	}

	return []byte(t.Text), t.MimeType, nil
}

// sweepDocTokens drops expired handles, so issuing handles never grows the
// store (or a snapshot) beyond those still valid.
func (m *Mock) sweepDocTokens() {
	for token, t := range m.docTokens.All() {
		if m.expired(t) {
			m.docTokens.Delete(token)
		}
	}
}

func (m *Mock) expired(t *docToken) bool {
	exp, err := time.Parse(time.RFC3339Nano, t.ExpiresAt)

	return err != nil || m.now().After(exp)
}

func findDocument(docs []badriver.IndexedDocument, dsID, documentID string) (badriver.IndexedDocument, bool) {
	for _, d := range docs {
		if d.DataSourceID == dsID && (d.Identifier.CustomID == documentID || d.Identifier.S3URI == documentID) {
			return d, true
		}
	}

	return badriver.IndexedDocument{}, false
}

package bedrockagent

import (
	"encoding/json"
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Sub-paths of {kb}/datasources/{ds}/documents for the POST read and delete
// operations.
const (
	docOpGet    = "getDocuments"
	docOpDelete = "deleteDocuments"
)

type customDocumentIDJSON struct {
	ID string `json:"id"`
}

type s3LocationJSON struct {
	URI string `json:"uri"`
}

type documentIdentifierJSON struct {
	DataSourceType string                `json:"dataSourceType"`
	Custom         *customDocumentIDJSON `json:"custom,omitempty"`
	S3             *s3LocationJSON       `json:"s3,omitempty"`
}

type documentDetailJSON struct {
	KnowledgeBaseID string                 `json:"knowledgeBaseId"`
	DataSourceID    string                 `json:"dataSourceId"`
	Status          string                 `json:"status"`
	Identifier      documentIdentifierJSON `json:"identifier"`
	StatusReason    string                 `json:"statusReason,omitempty"`
	UpdatedAt       string                 `json:"updatedAt,omitempty"`
}

type ingestDocumentsRequest struct {
	ClientToken string `json:"clientToken"`
	Documents   []struct {
		Content  json.RawMessage `json:"content"`
		Metadata json.RawMessage `json:"metadata"`
	} `json:"documents"`
}

type documentIDsRequest struct {
	ClientToken         string                   `json:"clientToken"`
	DocumentIdentifiers []documentIdentifierJSON `json:"documentIdentifiers"`
}

type documentDetailsResponse struct {
	DocumentDetails []documentDetailJSON `json:"documentDetails"`
	NextToken       string               `json:"nextToken,omitempty"`
}

// serveDocuments routes {kb}/datasources/{ds}/documents[/getDocuments|/deleteDocuments].
func serveDocuments(w http.ResponseWriter, r *http.Request, c badriver.KnowledgeBaseDocuments, segs []string) {
	kbID, dsID, ctx := segs[0], segs[2], r.Context()

	sub := ""
	if len(segs) == documentsOpSegments {
		sub = segs[4]
	}

	type route struct{ sub, method string }

	op := map[route]func(){
		{"", http.MethodPut}:  func() { ingestDocuments(w, r, c, kbID, dsID) },
		{"", http.MethodPost}: func() { listDocuments(w, r, c, kbID, dsID) },
		{docOpGet, http.MethodPost}: func() {
			if ids, ok := decodeDocumentIDs(w, r); ok {
				writeDocuments(w, http.StatusOK)(c.GetKnowledgeBaseDocuments(ctx, kbID, dsID, ids))
			}
		},
		{docOpDelete, http.MethodPost}: func() {
			if ids, ok := decodeDocumentIDs(w, r); ok {
				writeDocuments(w, http.StatusAccepted)(c.DeleteKnowledgeBaseDocuments(ctx, kbID, dsID, ids))
			}
		},
	}[route{sub, r.Method}]

	switch {
	case len(segs) > documentsOpSegments || (sub != "" && sub != docOpGet && sub != docOpDelete):
		notFound(w, r.URL.Path)
	case op == nil:
		methodNotAllowed(w)
	default:
		op()
	}
}

func ingestDocuments(w http.ResponseWriter, r *http.Request, c badriver.KnowledgeBaseDocuments, kbID, dsID string) {
	var in ingestDocumentsRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	docs := make([]badriver.KnowledgeBaseDocument, 0, len(in.Documents))
	for _, d := range in.Documents {
		docs = append(docs, badriver.KnowledgeBaseDocument{Content: d.Content, Metadata: d.Metadata})
	}

	writeDocuments(w, http.StatusAccepted)(c.IngestKnowledgeBaseDocuments(r.Context(), kbID, dsID, docs))
}

func listDocuments(w http.ResponseWriter, r *http.Request, c badriver.KnowledgeBaseDocuments, kbID, dsID string) {
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	docs, next, err := c.ListKnowledgeBaseDocuments(r.Context(), kbID, dsID, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, documentDetailsResponse{DocumentDetails: toDocumentDetailsJSON(docs), NextToken: next})
}

func decodeDocumentIDs(w http.ResponseWriter, r *http.Request) ([]badriver.DocumentIdentifier, bool) {
	var in documentIDsRequest
	if !decodeJSON(w, r, &in) {
		return nil, false
	}

	ids := make([]badriver.DocumentIdentifier, 0, len(in.DocumentIdentifiers))
	for _, id := range in.DocumentIdentifiers {
		ids = append(ids, fromDocumentIdentifierJSON(id))
	}

	return ids, true
}

func writeDocuments(w http.ResponseWriter, status int) func([]badriver.DocumentDetail, error) {
	return func(docs []badriver.DocumentDetail, err error) {
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, status, documentDetailsResponse{DocumentDetails: toDocumentDetailsJSON(docs)})
	}
}

func fromDocumentIdentifierJSON(id documentIdentifierJSON) badriver.DocumentIdentifier {
	out := badriver.DocumentIdentifier{DataSourceType: id.DataSourceType}

	if id.Custom != nil {
		out.CustomID = id.Custom.ID
	}

	if id.S3 != nil {
		out.S3URI = id.S3.URI
	}

	return out
}

func toDocumentDetailsJSON(docs []badriver.DocumentDetail) []documentDetailJSON {
	out := make([]documentDetailJSON, 0, len(docs))

	for i := range docs {
		d := &docs[i]
		id := documentIdentifierJSON{DataSourceType: d.Identifier.DataSourceType}

		switch d.Identifier.DataSourceType {
		case badriver.DocumentSourceCustom:
			id.Custom = &customDocumentIDJSON{ID: d.Identifier.CustomID}
		case badriver.DocumentSourceS3:
			id.S3 = &s3LocationJSON{URI: d.Identifier.S3URI}
		}

		out = append(out, documentDetailJSON{
			KnowledgeBaseID: d.KnowledgeBaseID, DataSourceID: d.DataSourceID, Status: d.Status,
			Identifier: id, StatusReason: d.StatusReason, UpdatedAt: d.UpdatedAt,
		})
	}

	return out
}

package driver

import "context"

// DocumentContent locates a knowledge-base document's content. Token is the
// opaque, expiring handle a presigned URL carries.
type DocumentContent struct {
	MimeType string
	Length   int64
	Token    string
}

// DocumentReader locates the content of ingested knowledge-base documents.
//
// Serving the content behind the returned token is up to the
// implementation (the AWS mock's ReadDocumentContent), since a presigned URL
// is not an API operation.
type DocumentReader interface {
	GetDocumentContent(ctx context.Context, kbID, dsID, documentID, format string) (*DocumentContent, error)
}

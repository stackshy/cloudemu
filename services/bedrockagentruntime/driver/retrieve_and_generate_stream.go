package driver

import "context"

// StreamingRetrieveAndGenerate answers RetrieveAndGenerateStream.
//
// It is the same generation as RetrieveAndGenerate, streamed as output and
// citation events.
type StreamingRetrieveAndGenerate interface {
	RetrieveAndGenerateStream(ctx context.Context, in RetrieveAndGenerateInput) (*RetrieveAndGenerateResult, error)
}

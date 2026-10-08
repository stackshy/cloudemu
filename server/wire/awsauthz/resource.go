package awsauthz

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// ARN returns the ARN of resource in service, in the scope's partition,
// region and account.
func (s Scope) ARN(service, resource string) string {
	return "arn:" + s.Partition + ":" + service + ":" + s.Region + ":" + s.AccountID + ":" + resource
}

// GlobalARN returns the ARN of resource in service for a resource with no
// region (IAM entities, DynamoDB global tables).
func (s Scope) GlobalARN(service, resource string) string {
	return "arn:" + s.Partition + ":" + service + "::" + s.AccountID + ":" + resource
}

// JSONBody decodes the request body into v the way the JSON-RPC handlers'
// dispatch does (encoding/json, first value only), and puts the body back so
// the handler reads the same bytes. It returns false when the body does not
// decode; the handler then answers with a serialization error and runs
// nothing.
func JSONBody(r *http.Request, v any) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	return json.NewDecoder(bytes.NewReader(raw)).Decode(v) == nil
}

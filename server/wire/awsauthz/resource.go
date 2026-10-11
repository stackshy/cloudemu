package awsauthz

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
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

// PartitionARN returns the ARN of resource in service for a resource with
// neither region nor account (Route 53 hosted zones and health checks).
func (s Scope) PartitionARN(service, resource string) string {
	return "arn:" + s.Partition + ":" + service + ":::" + resource
}

// JSONBody decodes the request body into v the way the JSON-RPC handlers'
// dispatch does (encoding/json, first value only), and puts the body back so
// the handler reads the same bytes. It returns false when the body does not
// decode; the handler then answers with a serialization error and runs
// nothing.
func JSONBody(r *http.Request, v any) bool {
	raw, ok := peekBody(r)

	return ok && json.NewDecoder(bytes.NewReader(raw)).Decode(v) == nil
}

// XMLBody decodes the request body into v the way the REST-XML handlers'
// dispatch does (encoding/xml, first element only), and puts the body back so
// the handler reads the same bytes. It returns false when the body does not
// decode; the handler then answers with a malformed-XML error and runs
// nothing.
func XMLBody(r *http.Request, v any) bool {
	raw, ok := peekBody(r)

	return ok && xml.NewDecoder(bytes.NewReader(raw)).Decode(v) == nil
}

// peekBody reads the whole request body and puts it back.
func peekBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, false
	}

	raw, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))

	return raw, err == nil
}

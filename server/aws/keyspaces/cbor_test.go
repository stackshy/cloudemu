package keyspaces_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/stackshy/cloudemu/v2/config"
	ksprovider "github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	ksserver "github.com/stackshy/cloudemu/v2/server/aws/keyspaces"
)

// cborClient sends raw rpc-v2-cbor requests the way aws-sdk-go-v2 keyspaces
// v1.37+ does (see its request_snapshot/*.request.snap).
type cborClient struct {
	t   *testing.T
	url string
}

func newCBORClient(t *testing.T) *cborClient {
	t.Helper()

	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	srv := httptest.NewServer(ksserver.NewCBOR(ksprovider.New(opts)))
	t.Cleanup(srv.Close)

	return &cborClient{t: t, url: srv.URL}
}

// call returns the status and the decoded CBOR body. Tag-1 timestamps decode
// as time.Time and integers as uint64/int64, so a test can check the types.
func (c *cborClient) call(op string, in map[string]any) (int, map[string]any) {
	c.t.Helper()

	payload, err := cbor.Marshal(in)
	if err != nil {
		c.t.Fatalf("%s: encode: %v", op, err)
	}

	req, err := http.NewRequest(http.MethodPost, c.url+"/service/KeyspacesService/operation/"+op, bytes.NewReader(payload))
	if err != nil {
		c.t.Fatalf("%s: request: %v", op, err)
	}

	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Accept", "application/cbor")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s: do: %v", op, err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Smithy-Protocol"); got != "rpc-v2-cbor" {
		c.t.Fatalf("%s: Smithy-Protocol header %q", op, got)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/cbor" {
		c.t.Fatalf("%s: Content-Type %q", op, got)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("%s: read: %v", op, err)
	}

	var out map[string]any
	if err := cbor.Unmarshal(raw, &out); err != nil {
		c.t.Fatalf("%s: decode %x: %v", op, raw, err)
	}

	return resp.StatusCode, out
}

func (c *cborClient) ok(op string, in map[string]any) map[string]any {
	c.t.Helper()

	status, out := c.call(op, in)
	if status != http.StatusOK {
		c.t.Fatalf("%s: status %d: %v", op, status, out)
	}

	return out
}

func cborSchema() map[string]any {
	return map[string]any{
		"allColumns":    []any{map[string]any{"name": "id", "type": "int"}},
		"partitionKeys": []any{map[string]any{"name": "id"}},
	}
}

// The rpc-v2-cbor form of the API serves the same operations as AWS JSON 1.0,
// with Integer/Long members as CBOR integers and Timestamp members as tag 1.
func TestCBORTableRoundTrip(t *testing.T) {
	c := newCBORClient(t)

	out := c.ok("CreateKeyspace", map[string]any{"keyspaceName": "app"})
	if out["resourceArn"] != "arn:aws:cassandra:us-east-1:123456789012:/keyspace/app/" {
		t.Fatalf("CreateKeyspace resourceArn: %v", out)
	}

	c.ok("CreateTable", map[string]any{
		"keyspaceName": "app", "tableName": "t1", "schemaDefinition": cborSchema(),
		"capacitySpecification": map[string]any{"throughputMode": "PROVISIONED", "readCapacityUnits": 5, "writeCapacityUnits": 7},
		"pointInTimeRecovery":   map[string]any{"status": "ENABLED"},
		"ttl":                   map[string]any{"status": "ENABLED"},
	})

	got := c.ok("GetTable", map[string]any{"keyspaceName": "app", "tableName": "t1"})

	if _, ok := got["creationTimestamp"].(time.Time); !ok {
		t.Fatalf("creationTimestamp = %T %v, want a tag-1 timestamp", got["creationTimestamp"], got["creationTimestamp"])
	}

	capSpec, _ := got["capacitySpecification"].(map[any]any)
	if capSpec["readCapacityUnits"] != uint64(5) || capSpec["writeCapacityUnits"] != uint64(7) {
		t.Fatalf("capacitySpecification = %#v, want integer units 5/7", capSpec)
	}

	if ttl, _ := got["ttl"].(map[any]any); ttl["status"] != "ENABLED" {
		t.Fatalf("ttl = %v", got["ttl"])
	}

	if _, ok := got["clientSideTimestamps"]; ok {
		t.Fatalf("clientSideTimestamps present on a table that never set it: %v", got)
	}

	// A tag-1 restoreTimestamp decodes like the JSON epoch number.
	c.ok("RestoreTable", map[string]any{
		"sourceKeyspaceName": "app", "sourceTableName": "t1",
		"targetKeyspaceName": "app", "targetTableName": "t2",
		"restoreTimestamp": time.Now().Add(-time.Minute),
	})

	list := c.ok("ListTables", map[string]any{"keyspaceName": "app"})
	if tables, _ := list["tables"].([]any); len(tables) != 2 {
		t.Fatalf("ListTables = %v, want 2 tables", list)
	}

	c.ok("DeleteTable", map[string]any{"keyspaceName": "app", "tableName": "t2"})
}

// Errors keep the JSON protocol's status and code, as a CBOR __type/message
// map.
func TestCBORErrors(t *testing.T) {
	c := newCBORClient(t)

	cases := []struct {
		name, op string
		in       map[string]any
		status   int
		code     string
	}{
		{"missing table", "GetTable", map[string]any{"keyspaceName": "nope", "tableName": "t"}, http.StatusBadRequest, "ResourceNotFoundException"},
		{"bad name", "CreateKeyspace", map[string]any{"keyspaceName": "bad-name"}, http.StatusBadRequest, "ValidationException"},
		{"future restore", "RestoreTable", map[string]any{
			"sourceKeyspaceName": "a", "sourceTableName": "b", "targetKeyspaceName": "a", "targetTableName": "c",
			"restoreTimestamp": time.Now().Add(time.Hour),
		}, http.StatusBadRequest, "ValidationException"},
		{"unknown operation", "Frobnicate", map[string]any{}, http.StatusBadRequest, "ValidationException"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, out := c.call(tc.op, tc.in)
			if status != tc.status || out["__type"] != tc.code {
				t.Fatalf("status %d body %v, want %d %s", status, out, tc.status, tc.code)
			}

			if msg, _ := out["message"].(string); msg == "" {
				t.Fatalf("empty message: %v", out)
			}
		})
	}
}

func TestCBORRejectsMalformedBody(t *testing.T) {
	c := newCBORClient(t)

	req, _ := http.NewRequest(http.MethodPost, c.url+"/service/KeyspacesService/operation/ListKeyspaces", bytes.NewReader([]byte{0xff, 0x00}))
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	req.Header.Set("Content-Type", "application/cbor")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := cbor.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.StatusCode != http.StatusBadRequest || out["__type"] != "SerializationException" {
		t.Fatalf("status %d body %v, want 400 SerializationException", resp.StatusCode, out)
	}
}

func TestCBORMatches(t *testing.T) {
	h := ksserver.NewCBOR(nil)

	cases := []struct {
		name, path, proto string
		want              bool
	}{
		{"keyspaces cbor", "/service/KeyspacesService/operation/GetTable", "rpc-v2-cbor", true},
		{"cloudwatch cbor", "/service/GraniteServiceVersion20100801/operation/ListMetrics", "rpc-v2-cbor", false},
		{"keyspaces path without the protocol header", "/service/KeyspacesService/operation/GetTable", "", false},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		if tc.proto != "" {
			req.Header.Set("Smithy-Protocol", tc.proto)
		}

		if got := h.Matches(req); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

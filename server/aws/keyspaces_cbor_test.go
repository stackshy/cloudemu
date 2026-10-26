package aws

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"

	cloudemu "github.com/stackshy/cloudemu/v2"
)

// keyspacesCBOR is a Keyspaces rpc-v2-cbor request as aws-sdk-go-v2 keyspaces
// v1.37+ (hashicorp/aws 6.69+) sends it.
func keyspacesCBOR(t *testing.T, op string, in map[string]any) sreq {
	t.Helper()

	payload, err := cbor.Marshal(in)
	if err != nil {
		t.Fatalf("cbor: %v", err)
	}

	return sreq{
		path: "/service/KeyspacesService/operation/" + op, ctype: "application/cbor", body: string(payload),
		service: "cassandra", header: map[string]string{"Smithy-Protocol": "rpc-v2-cbor"},
	}
}

func decodeCBORMap(t *testing.T, body string) map[string]any {
	t.Helper()

	var out map[string]any
	if err := cbor.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	return out
}

// The full AWS server routes Keyspaces rpc-v2-cbor to Keyspaces, not to
// CloudWatch, which claims every other rpc-v2-cbor request.
func TestKeyspacesCBORDispatch(t *testing.T) {
	ts := httptest.NewServer(New(DriversFrom(cloudemu.NewAWS())))
	t.Cleanup(ts.Close)

	rq := keyspacesCBOR(t, "CreateKeyspace", map[string]any{"keyspaceName": "app"})

	req, err := http.NewRequest(http.MethodPost, ts.URL+rq.path, bytes.NewReader([]byte(rq.body)))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Content-Type", rq.ctype)
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := cbor.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.StatusCode != http.StatusOK || out["resourceArn"] == nil {
		t.Fatalf("CreateKeyspace over rpc-v2-cbor: %d %v", resp.StatusCode, out)
	}
}

// Under --enforce-auth a Keyspaces rpc-v2-cbor request is authorized as the
// cassandra:<Operation> named in its path, and a deny is a CBOR error.
func TestKeyspacesCBORAuthorization(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	lister := userWithPolicy(t, cloud, "kslister", allow("cassandra:ListKeyspaces"))

	status, body := doSigned(t, ts, lister, keyspacesCBOR(t, "ListKeyspaces", map[string]any{}))
	if status != http.StatusOK {
		t.Fatalf("ListKeyspaces: %d %v", status, decodeCBORMap(t, body))
	}

	status, body = doSigned(t, ts, lister, keyspacesCBOR(t, "CreateKeyspace", map[string]any{"keyspaceName": "app"}))
	if out := decodeCBORMap(t, body); status != http.StatusForbidden || out["__type"] != accessDeny {
		t.Fatalf("CreateKeyspace: %d %v, want 403 %s", status, out, accessDeny)
	}

	if _, err := cloud.Keyspaces.GetKeyspace(t.Context(), "app"); err == nil {
		t.Fatal("a denied CreateKeyspace created the keyspace")
	}
}

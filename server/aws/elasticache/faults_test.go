package elasticache

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ecprovider "github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
)

// testFault is a stand-in for the provider's typed Fault.
type testFault struct {
	err  *cerrors.Error
	code string
}

func (f testFault) Error() string    { return f.err.Error() }
func (f testFault) Unwrap() error    { return f.err }
func (f testFault) AWSFault() string { return f.code }

type xmlErrorBody struct {
	Code    string `xml:"Error>Code"`
	Message string `xml:"Error>Message"`
}

func recordWriteErr(t *testing.T, err error) (status int, body xmlErrorBody) {
	t.Helper()

	rec := httptest.NewRecorder()
	writeErr(rec, err)

	if uerr := xml.Unmarshal(rec.Body.Bytes(), &body); uerr != nil {
		t.Fatalf("unmarshal error body %q: %v", rec.Body.String(), uerr)
	}

	return rec.Code, body
}

// allFaultCodes lists every fault code the provider emits.
func allFaultCodes() []string {
	return []string{
		ecprovider.FaultCacheClusterNotFound, ecprovider.FaultCacheClusterAlreadyExists,
		ecprovider.FaultInvalidCacheClusterState, ecprovider.FaultReplicationGroupNotFound,
		ecprovider.FaultReplicationGroupAlreadyExists, ecprovider.FaultInvalidReplicationGroupState,
		ecprovider.FaultCacheSubnetGroupNotFound, ecprovider.FaultCacheSubnetGroupAlreadyExists,
		ecprovider.FaultCacheSubnetGroupInUse, ecprovider.FaultCacheParameterGroupNotFound,
		ecprovider.FaultCacheParameterGroupAlreadyExists, ecprovider.FaultInvalidCacheParameterGroupState,
		ecprovider.FaultSnapshotNotFound, ecprovider.FaultSnapshotAlreadyExists,
		ecprovider.FaultSnapshotFeatureNotSupported,
	}
}

// TestWriteErrStripsCodeFromMessage proves the wire <Message> carries only the
// human text: neither the canonical taxonomy token ("NotFound: ") nor the
// fault code itself, as a real ElastiCache message never starts with one.
func TestWriteErrStripsCodeFromMessage(t *testing.T) {
	for _, code := range allFaultCodes() {
		t.Run(code, func(t *testing.T) {
			err := testFault{err: cerrors.New(cerrors.NotFound, "resource \"x\" not found"), code: code}

			_, body := recordWriteErr(t, err)

			if body.Code != code {
				t.Fatalf("Code = %q, want %q", body.Code, code)
			}

			if body.Message != `resource "x" not found` {
				t.Fatalf("Message = %q, want the bare human message", body.Message)
			}

			if strings.HasPrefix(body.Message, code) || strings.HasPrefix(body.Message, "NotFound") {
				t.Fatalf("Message %q leaks a code prefix", body.Message)
			}
		})
	}
}

// TestWriteErrStatusFromTable checks the HTTP status per fault code against the
// botocore model, including CacheSubnetGroupNotFoundFault answering 400.
func TestWriteErrStatusFromTable(t *testing.T) {
	notFound := map[string]bool{
		ecprovider.FaultCacheClusterNotFound:        true,
		ecprovider.FaultReplicationGroupNotFound:    true,
		ecprovider.FaultCacheParameterGroupNotFound: true,
		ecprovider.FaultSnapshotNotFound:            true,
	}

	for _, code := range allFaultCodes() {
		want := http.StatusBadRequest
		if notFound[code] {
			want = http.StatusNotFound
		}

		status, _ := recordWriteErr(t, testFault{err: cerrors.New(cerrors.NotFound, "m"), code: code})
		if status != want {
			t.Errorf("%s: status %d, want %d", code, status, want)
		}
	}

	status, body := recordWriteErr(t, cerrors.New(cerrors.InvalidArgument, "bad input"))
	if status != http.StatusBadRequest || body.Code != "InvalidParameterValue" || body.Message != "bad input" {
		t.Fatalf("plain InvalidArgument = %d %+v, want 400 InvalidParameterValue", status, body)
	}
}

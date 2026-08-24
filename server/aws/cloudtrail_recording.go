package aws

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cloudtraildriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// recordManagementEvent derives a CloudTrail management event from a served
// request and records it. It runs as the server's post-dispatch observer, so
// LookupEvents reflects real API activity. Requests whose operation cannot be
// determined (e.g. an unrecognized shape) are skipped rather than logged blank.
func recordManagementEvent(rec cloudtraildriver.EventRecorder, r *http.Request) {
	name := requestEventName(r)
	if name == "" {
		return
	}

	auth := r.Header.Get("Authorization")

	source := ""
	if svc := awsquery.CredentialScopeService(auth); svc != "" {
		source = svc + ".amazonaws.com"
	}

	rec.RecordEvent(&cloudtraildriver.Event{
		EventName:   name,
		EventSource: source,
		AccessKeyID: credentialAccessKeyID(auth),
		ReadOnly:    strconv.FormatBool(isReadOnlyEvent(name)),
	})
}

// requestEventName extracts the API operation name (the CloudTrail EventName)
// from a request: the operation after the prefix in an X-Amz-Target JSON-RPC
// header, else the Action parameter of a query-protocol request.
func requestEventName(r *http.Request) string {
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		if i := strings.LastIndex(target, "."); i >= 0 && i+1 < len(target) {
			return target[i+1:]
		}

		return target
	}

	if a := r.Form.Get("Action"); a != "" {
		return a
	}

	return r.URL.Query().Get("Action")
}

// credentialAccessKeyID extracts the access-key id from a SigV4 Authorization
// header's credential scope (Credential=AKID/DATE/REGION/SERVICE/aws4_request).
func credentialAccessKeyID(auth string) string {
	i := strings.Index(auth, "Credential=")
	if i < 0 {
		return ""
	}

	rest := auth[i+len("Credential="):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}

	return ""
}

// isReadOnlyEvent classifies an operation as read-only by its verb prefix, the
// same heuristic CloudTrail's readOnly flag reflects for management events.
func isReadOnlyEvent(name string) bool {
	for _, prefix := range []string{"Describe", "List", "Get", "Lookup", "Search", "BatchGet", "Query", "Scan", "Head"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

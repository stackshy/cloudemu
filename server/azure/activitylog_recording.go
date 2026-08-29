package azure

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// defaultActivityCaller is the caller recorded when a request carries no
// identity the emulator can attribute (auth is accepted but not verified).
const defaultActivityCaller = "cloudemu@localhost"

// recordActivityLogEvent derives an Azure Activity Log management event from a
// served ARM request and records it. It runs as the server's post-dispatch
// observer, so the Activity Log API reflects real API activity. Read-only
// requests (GET/HEAD) and non-ARM (data-plane) URLs are skipped — Activity
// Log's Administrative category records writes/deletes/actions, not reads.
func recordActivityLogEvent(rec mondriver.ActivityLogRecorder, r *http.Request) {
	verb := writeVerb(r.Method)
	if verb == "" {
		return
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok || rp.Provider == "" || rp.ResourceType == "" {
		return
	}

	// The Activity Log itself is a read API; don't record its own reads (they're
	// GETs and already filtered out), and never record microsoft.insights
	// activity-log queries even if a client POSTs to them.
	if strings.EqualFold(rp.Provider, "Microsoft.Insights") {
		return
	}

	rec.RecordActivityLogEvent(&mondriver.ActivityLogEvent{
		OperationName:    rp.Provider + "/" + rp.ResourceType + "/" + verb,
		ResourceID:       strings.TrimRight(r.URL.Path, "/"),
		ResourceProvider: rp.Provider,
		ResourceGroup:    rp.ResourceGroup,
		SubscriptionID:   rp.Subscription,
		Caller:           defaultActivityCaller,
	})
}

// writeVerb maps an HTTP method to the ARM operation verb the Activity Log
// records, or "" for read-only methods that aren't recorded.
func writeVerb(method string) string {
	switch method {
	case http.MethodPut, http.MethodPatch:
		return "write"
	case http.MethodDelete:
		return "delete"
	case http.MethodPost:
		return "action"
	default:
		return ""
	}
}

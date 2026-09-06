package appinsights

import (
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// The instrumentation key and app id are GUID-shaped values Azure computes ONCE,
// at create. Deriving them deterministically from the component's ARM id (via the
// shared ETag helper, which already produces a stable GUID-shaped token) makes
// them reproducible, and storing them on the state at create keeps them byte-for-
// byte identical on every later GET — the property the REST contract guarantees
// ("you cannot specify a different value for InstrumentationKey nor AppId in the
// Put operation") and the #1 Terraform-drift source if it were regenerated.

func newInstrumentationKey(id string) string { return azurearm.ETag("appinsights-ikey", id) }

func newAppID(id string) string { return azurearm.ETag("appinsights-appid", id) }

// newTenantID derives a stable synthetic tenant GUID per subscription, so every
// component in a subscription reports the same TenantId across reads.
func newTenantID(subscription string) string {
	return azurearm.ETag("appinsights-tenant", subscription)
}

// nowISO8601 formats the current instant in the offset form Azure emits for
// CreationDate (e.g. 2017-01-24T01:05:38.5934061+00:00).
func nowISO8601() string { return time.Now().UTC().Format("2006-01-02T15:04:05.9999999-07:00") }

// cloneTags copies a request tag map so the store never aliases the request
// body; a nil or empty map stores nil (an explicit tags:{} clears the set).
func cloneTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

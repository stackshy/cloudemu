package mwaa

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

// epochOrNil renders a time as a Unix-epoch-seconds float the MWAA SDK decodes
// into a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// environmentToWire renders a full environment as its GetEnvironment restJson1
// Environment object. The verbatim configuration blocks are emitted first so
// the computed fields always win.
func environmentToWire(e *driver.Environment) map[string]any {
	out := map[string]any{}
	for k, v := range e.Config {
		out[k] = v
	}

	out["Name"] = e.Name
	out["Arn"] = e.Arn
	out["Status"] = e.Status
	out["WebserverUrl"] = e.WebserverURL
	out["ServiceRoleArn"] = e.ServiceRoleArn
	out["CreatedAt"] = epochOrNil(e.CreatedAt)
	out["LastUpdate"] = map[string]any{
		"CreatedAt": epochOrNil(e.LastUpdatedAt),
		"Status":    driver.UpdateStatusSuccess,
		"Source":    "cloudemu",
	}

	if e.Tags != nil {
		out["Tags"] = e.Tags
	}

	return out
}

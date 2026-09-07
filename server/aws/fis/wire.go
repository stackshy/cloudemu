package fis

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// writeListPage renders a {<key>: [...summaries], nextToken?} list response,
// centralizing the mechanics the template and experiment list operations share.
func writeListPage[T any](w http.ResponseWriter, key string, items []*T, next string, toWire func(*T) map[string]any) {
	summaries := make([]map[string]any, 0, len(items))
	for _, it := range items {
		summaries = append(summaries, toWire(it))
	}

	body := map[string]any{key: summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}

// millisPerSecond converts milliseconds to the fractional epoch seconds FIS
// timestamps use on the wire.
const millisPerSecond = 1000.0

// epochSeconds renders a timestamp as restJson1 epoch seconds (a JSON number),
// or nil for the zero time so an unset timestamp reads back as null.
func epochSeconds(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return float64(t.UTC().UnixMilli()) / millisPerSecond
}

// putNonEmpty sets key to val only when val is non-empty.
func putNonEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// putStrings sets key to a string list only when the list is non-empty.
func putStrings(m map[string]any, key string, vals []string) {
	if len(vals) > 0 {
		m[key] = vals
	}
}

// putStringMap sets key to a string map only when it is non-empty.
func putStringMap(m map[string]any, key string, vals map[string]string) {
	if len(vals) > 0 {
		m[key] = vals
	}
}

// targetToWire renders an experiment target block verbatim.
func targetToWire(t *driver.Target) map[string]any {
	out := map[string]any{}
	putNonEmpty(out, "resourceType", t.ResourceType)
	putNonEmpty(out, "selectionMode", t.SelectionMode)
	putStrings(out, "resourceArns", t.ResourceArns)
	putStringMap(out, "resourceTags", t.ResourceTags)
	putStringMap(out, "parameters", t.Parameters)

	if len(t.Filters) > 0 {
		filters := make([]map[string]any, 0, len(t.Filters))

		for i := range t.Filters {
			f := map[string]any{}
			putNonEmpty(f, "path", t.Filters[i].Path)
			putStrings(f, "values", t.Filters[i].Values)
			filters = append(filters, f)
		}

		out["filters"] = filters
	}

	return out
}

// targetsToWire renders the targets map, or nil when empty.
func targetsToWire(in map[string]driver.Target) map[string]any {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]any, len(in))

	for name := range in {
		t := in[name]
		out[name] = targetToWire(&t)
	}

	return out
}

// templateActionToWire renders a template action block verbatim.
func templateActionToWire(a driver.Action) map[string]any {
	out := map[string]any{}
	putNonEmpty(out, "actionId", a.ActionID)
	putNonEmpty(out, "description", a.Description)
	putStringMap(out, "parameters", a.Parameters)
	putStringMap(out, "targets", a.Targets)
	putStrings(out, "startAfter", a.StartAfter)

	return out
}

// templateActionsToWire renders the template actions map, or nil when empty.
func templateActionsToWire(in map[string]driver.Action) map[string]any {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]any, len(in))

	for name, a := range in {
		out[name] = templateActionToWire(a)
	}

	return out
}

// stopConditionsToWire renders the stop conditions list verbatim.
func stopConditionsToWire(in []driver.StopCondition) []map[string]any {
	out := make([]map[string]any, 0, len(in))

	for i := range in {
		sc := map[string]any{}
		putNonEmpty(sc, "source", in[i].Source)
		putNonEmpty(sc, "value", in[i].Value)
		out = append(out, sc)
	}

	return out
}

// logConfigurationToWire renders the log configuration, or nil when unset.
func logConfigurationToWire(lc *driver.LogConfiguration) map[string]any {
	if lc == nil {
		return nil
	}

	out := map[string]any{}
	if lc.LogSchemaVersion != 0 {
		out["logSchemaVersion"] = lc.LogSchemaVersion
	}

	if lc.CloudWatchLogsConfiguration != nil {
		out["cloudWatchLogsConfiguration"] = map[string]any{
			"logGroupArn": lc.CloudWatchLogsConfiguration.LogGroupArn,
		}
	}

	if lc.S3Configuration != nil {
		s3 := map[string]any{"bucketName": lc.S3Configuration.BucketName}
		putNonEmpty(s3, "prefix", lc.S3Configuration.Prefix)
		out["s3Configuration"] = s3
	}

	return out
}

// experimentOptionsToWire renders the experiment options. includeActionsMode
// controls whether the experiment-only actionsMode field is emitted.
func experimentOptionsToWire(o driver.ExperimentOptions, includeActionsMode bool) map[string]any {
	out := map[string]any{}
	putNonEmpty(out, "accountTargeting", o.AccountTargeting)
	putNonEmpty(out, "emptyTargetResolutionMode", o.EmptyTargetResolutionMode)

	if includeActionsMode {
		putNonEmpty(out, "actionsMode", o.ActionsMode)
	}

	return out
}

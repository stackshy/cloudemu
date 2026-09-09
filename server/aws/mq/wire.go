package mq

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// iso8601OrNil renders a time as an ISO-8601 string the MQ SDK decodes into a
// *time.Time, or nil for the zero time.
func iso8601OrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}

	s := t.UTC().Format(time.RFC3339)

	return &s
}

// brokerToWire renders a full broker as its DescribeBroker restJson1 object. The
// verbatim configuration fields are emitted first so the computed fields always
// win.
func brokerToWire(b *driver.Broker) map[string]any {
	out := map[string]any{}
	for k, v := range b.Config {
		out[k] = v
	}

	out["brokerId"] = b.BrokerID
	out["brokerArn"] = b.BrokerArn
	out["brokerName"] = b.BrokerName
	out["brokerState"] = b.BrokerState
	out["created"] = iso8601OrNil(b.Created)
	out["brokerInstances"] = instancesToWire(b.Instances)
	out["users"] = usersSummaryToWire(b.Users)

	if b.Configuration != nil {
		out["configurations"] = configurationsBlock(b.Configuration)
	}

	if b.Tags != nil {
		out["tags"] = b.Tags
	}

	return out
}

// instancesToWire renders the per-instance consoleUrl/endpoints/ipAddress list.
func instancesToWire(insts []driver.Instance) []map[string]any {
	out := make([]map[string]any, 0, len(insts))

	for i := range insts {
		m := map[string]any{
			"consoleURL": insts[i].ConsoleURL,
			"endpoints":  insts[i].Endpoints,
		}
		if insts[i].IPAddress != "" {
			m["ipAddress"] = insts[i].IPAddress
		}

		out = append(out, m)
	}

	return out
}

// usersSummaryToWire renders the broker's users as UserSummary objects
// (usernames only); passwords are never included.
func usersSummaryToWire(users []driver.User) []map[string]any {
	out := make([]map[string]any, 0, len(users))
	for i := range users {
		out = append(out, map[string]any{"username": users[i].Username})
	}

	return out
}

// configurationsBlock renders the broker's Configurations block (current plus a
// single-entry history) from its current configuration reference.
func configurationsBlock(ref *driver.ConfigRef) map[string]any {
	cur := configRefToWire(ref)

	return map[string]any{
		"current": cur,
		"history": []map[string]any{cur},
	}
}

// configRefToWire renders a configuration reference as {id, revision}.
func configRefToWire(ref *driver.ConfigRef) map[string]any {
	return map[string]any{"id": ref.ID, "revision": ref.Revision}
}

// configurationToWire renders a configuration as its DescribeConfiguration
// restJson1 object.
func configurationToWire(c *driver.Configuration) map[string]any {
	out := map[string]any{
		"id":                     c.ID,
		"arn":                    c.Arn,
		"name":                   c.Name,
		"engineType":             c.EngineType,
		"engineVersion":          c.EngineVersion,
		"authenticationStrategy": c.AuthenticationStrategy,
		"created":                iso8601OrNil(c.Created),
		"latestRevision":         revisionToWire(c.LatestRevision()),
	}

	if c.Description != "" {
		out["description"] = c.Description
	}

	if c.Tags != nil {
		out["tags"] = c.Tags
	}

	return out
}

// revisionToWire renders a configuration revision's metadata (no data body).
func revisionToWire(r *driver.ConfigurationRevision) map[string]any {
	if r == nil {
		return nil
	}

	out := map[string]any{
		"revision": r.Revision,
		"created":  iso8601OrNil(r.Created),
	}
	if r.Description != "" {
		out["description"] = r.Description
	}

	return out
}

// brokerSummaryToWire renders a broker as its ListBrokers BrokerSummary object.
// The descriptive fields are read back from the stored verbatim configuration.
func brokerSummaryToWire(b *driver.Broker) map[string]any {
	out := map[string]any{
		"brokerId":    b.BrokerID,
		"brokerArn":   b.BrokerArn,
		"brokerName":  b.BrokerName,
		"brokerState": b.BrokerState,
		"created":     iso8601OrNil(b.Created),
	}

	for _, k := range []string{"engineType", "deploymentMode", "hostInstanceType"} {
		if v, ok := b.Config[k]; ok {
			out[k] = v
		}
	}

	return out
}

// tagsFromBody extracts the modeled tags map from a raw request body.
func tagsFromBody(raw map[string]json.RawMessage) map[string]string {
	v, ok := raw["tags"]
	if !ok {
		return nil
	}

	var tags map[string]string
	if json.Unmarshal(v, &tags) != nil {
		return nil
	}

	return tags
}

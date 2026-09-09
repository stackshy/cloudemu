package codeartifact

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// epochSeconds renders a time as the fractional-second Unix epoch number the
// CodeArtifact SDK decodes into a time.Time, or nil for the zero time.
func epochSeconds(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return float64(t.Unix())
}

// domainToWire renders a full domain as its DomainDescription restJson1 object.
func domainToWire(d *driver.Domain) map[string]any {
	out := map[string]any{
		"name":            d.Name,
		"owner":           d.Owner,
		"arn":             d.Arn,
		"status":          d.Status,
		"createdTime":     epochSeconds(d.CreatedTime),
		"assetSizeBytes":  d.AssetSizeBytes,
		"repositoryCount": d.RepositoryCount,
	}

	if d.EncryptionKey != "" {
		out["encryptionKey"] = d.EncryptionKey
	}

	if d.S3BucketArn != "" {
		out["s3BucketArn"] = d.S3BucketArn
	}

	return out
}

// domainSummaryToWire renders a domain as its ListDomains DomainSummary object.
func domainSummaryToWire(d *driver.Domain) map[string]any {
	out := map[string]any{
		"name":        d.Name,
		"owner":       d.Owner,
		"arn":         d.Arn,
		"status":      d.Status,
		"createdTime": epochSeconds(d.CreatedTime),
	}

	if d.EncryptionKey != "" {
		out["encryptionKey"] = d.EncryptionKey
	}

	return out
}

// repositoryToWire renders a full repository as its RepositoryDescription
// restJson1 object.
func repositoryToWire(r *driver.Repository) map[string]any {
	out := map[string]any{
		"name":                 r.Name,
		"administratorAccount": r.AdministratorAccount,
		"domainName":           r.DomainName,
		"domainOwner":          r.DomainOwner,
		"arn":                  r.Arn,
		"createdTime":          epochSeconds(r.CreatedTime),
		"upstreams":            upstreamsToWire(r.Upstreams),
		"externalConnections":  externalConnsToWire(r.ExternalConnections),
	}

	if r.Description != "" {
		out["description"] = r.Description
	}

	return out
}

// repositorySummaryToWire renders a repository as its ListRepositories
// RepositorySummary object (no upstreams or external connections).
func repositorySummaryToWire(r *driver.Repository) map[string]any {
	out := map[string]any{
		"name":                 r.Name,
		"administratorAccount": r.AdministratorAccount,
		"domainName":           r.DomainName,
		"domainOwner":          r.DomainOwner,
		"arn":                  r.Arn,
		"createdTime":          epochSeconds(r.CreatedTime),
	}

	if r.Description != "" {
		out["description"] = r.Description
	}

	return out
}

// upstreamsToWire renders the repository's upstream references.
func upstreamsToWire(ups []driver.UpstreamRef) []map[string]any {
	out := make([]map[string]any, 0, len(ups))
	for i := range ups {
		out = append(out, map[string]any{"repositoryName": ups[i].RepositoryName})
	}

	return out
}

// externalConnsToWire renders the repository's external connections.
func externalConnsToWire(conns []driver.ExternalConnection) []map[string]any {
	out := make([]map[string]any, 0, len(conns))
	for i := range conns {
		out = append(out, map[string]any{
			"externalConnectionName": conns[i].ExternalConnectionName,
			"packageFormat":          conns[i].PackageFormat,
			"status":                 conns[i].Status,
		})
	}

	return out
}

// tagsToWire renders a tag map as the CodeArtifact list of {key, value} objects,
// ordered by key for a deterministic response.
func tagsToWire(tags map[string]string) []map[string]any {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]map[string]any, 0, len(tags))
	for _, k := range keys {
		out = append(out, map[string]any{"key": k, "value": tags[k]})
	}

	return out
}

// tagPair mirrors one CodeArtifact Tag object in a request body.
type tagPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// tagsFromBody extracts the tags list from a raw request body into a map.
func tagsFromBody(raw map[string]json.RawMessage) map[string]string {
	v, ok := raw["tags"]
	if !ok {
		return nil
	}

	var list []tagPair
	if json.Unmarshal(v, &list) != nil {
		return nil
	}

	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}

	return out
}

// upstreamsFromBody extracts the upstreams list from a raw request body, or nil
// when the field is absent.
func upstreamsFromBody(raw map[string]json.RawMessage) *[]driver.UpstreamRef {
	v, ok := raw["upstreams"]
	if !ok {
		return nil
	}

	var list []struct {
		RepositoryName string `json:"repositoryName"`
	}

	if json.Unmarshal(v, &list) != nil {
		return nil
	}

	out := make([]driver.UpstreamRef, 0, len(list))
	for _, u := range list {
		out = append(out, driver.UpstreamRef{RepositoryName: u.RepositoryName})
	}

	return &out
}

// stringPtrFromBody returns a pointer to a string body field when present, or nil
// when the field is absent (so an update leaves it unchanged).
func stringPtrFromBody(raw map[string]json.RawMessage, key string) *string {
	v, ok := raw[key]
	if !ok {
		return nil
	}

	var s string
	if json.Unmarshal(v, &s) != nil {
		return nil
	}

	return &s
}

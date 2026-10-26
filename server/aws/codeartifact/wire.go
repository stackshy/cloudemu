package codeartifact

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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

// tagsToMap converts a request's tag list to a map.
func tagsToMap(list []tagPair) map[string]string {
	if list == nil {
		return nil
	}

	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}

	return out
}

// upstreamPair mirrors one UpstreamRepository object in a request body.
type upstreamPair struct {
	RepositoryName string `json:"repositoryName"`
}

// upstreamsFromList converts a request's upstream list to driver references.
func upstreamsFromList(list []upstreamPair) []driver.UpstreamRef {
	out := make([]driver.UpstreamRef, 0, len(list))
	for _, u := range list {
		out = append(out, driver.UpstreamRef{RepositoryName: u.RepositoryName})
	}

	return out
}

// queryTrue is the value of a boolean query parameter that is set.
const queryTrue = "true"

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow; the driver then rejects anything above the API's own limit.
const maxQueryInt = 1 << 20

// queryInt32 parses an optional non-negative integer query parameter. An absent
// parameter is 0; a malformed or negative one is a ValidationException.
func queryInt32(w http.ResponseWriter, q url.Values, key string) (int32, bool) {
	s := q.Get(key)
	if s == "" {
		return 0, true
	}

	n, err := strconv.ParseUint(s, 10, 31)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		writeValidation(w, key+" must be a non-negative integer")

		return 0, false
	}

	// A value beyond 31 bits is clamped: the driver rejects anything above the API's own limit.
	return int32(min(n, maxQueryInt)), true //nolint:gosec // clamped to maxQueryInt, far below MaxInt32
}

// pageFromQuery reads the max-results and next-token query parameters.
func pageFromQuery(w http.ResponseWriter, q url.Values) (driver.Page, bool) {
	n, ok := queryInt32(w, q, "max-results")
	if !ok {
		return driver.Page{}, false
	}

	if q.Has("max-results") && n == 0 {
		writeValidation(w, "max-results must be at least 1")

		return driver.Page{}, false
	}

	return driver.Page{MaxResults: n, NextToken: q.Get("next-token")}, true
}

func domainRefFromQuery(q url.Values) driver.DomainRef {
	return driver.DomainRef{Domain: q.Get("domain"), Owner: q.Get("domain-owner")}
}

func repoRefFromQuery(q url.Values) driver.RepositoryRef {
	return driver.RepositoryRef{Domain: q.Get("domain"), Owner: q.Get("domain-owner"), Repository: q.Get("repository")}
}

func pkgRefFromQuery(q url.Values) driver.PackageRef {
	return driver.PackageRef{
		Domain: q.Get("domain"), Owner: q.Get("domain-owner"), Repository: q.Get("repository"),
		Format: q.Get("format"), Namespace: q.Get("namespace"), Package: q.Get("package"),
	}
}

// putNonEmpty sets m[key] = v unless v is empty.
func putNonEmpty(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func newCreateDomainInput(domain string, req *createDomainRequest) *driver.CreateDomainInput {
	return &driver.CreateDomainInput{Name: domain, EncryptionKey: req.EncryptionKey, Tags: tagsToMap(req.Tags)}
}

// pageFromBody reads the paging members of a request body. A maxResults that is
// present but not positive is rejected; an absent one means the default page size.
func pageFromBody(w http.ResponseWriter, req *listPageRequest) (driver.Page, bool) {
	if req.MaxResults != nil && *req.MaxResults == 0 {
		writeValidation(w, "maxResults must be at least 1")

		return driver.Page{}, false
	}

	page := driver.Page{NextToken: req.NextToken}
	if req.MaxResults != nil {
		page.MaxResults = *req.MaxResults
	}

	return page, true
}

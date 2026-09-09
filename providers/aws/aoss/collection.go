package aoss

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// validCollectionTypes is the set of collection types the API accepts.
//
//nolint:gochecknoglobals // static validation set
var validCollectionTypes = map[string]bool{
	driver.CollectionTypeSearch:       true,
	driver.CollectionTypeTimeSeries:   true,
	driver.CollectionTypeVectorSearch: true,
}

// CreateCollection provisions a collection directly in the ACTIVE state with
// stable computed fields (id, arn, endpoints, createdDate). A matching
// encryption security policy must already exist, mirroring real OpenSearch
// Serverless, which rejects a collection with no encryption policy covering its
// name. The kmsKeyArn is resolved from that policy.
func (m *Mock) CreateCollection(_ context.Context, in *driver.CreateCollectionInput) (*driver.Collection, error) {
	if in.Name == "" {
		return nil, validation("name is required")
	}

	colType := in.Type
	if colType == "" {
		colType = driver.CollectionTypeTimeSeries
	}

	if !validCollectionTypes[colType] {
		return nil, validation("invalid collection type: %q", in.Type)
	}

	if m.collectionNameExists(in.Name) {
		return nil, conflict("collection with name %q already exists", in.Name)
	}

	kmsKeyArn, ok := m.resolveEncryptionKey(in.Name)
	if !ok {
		return nil, validation(
			"no matching encryption policy for collection %q; an encryption security policy covering the collection is required",
			in.Name)
	}

	standby := in.StandbyReplicas
	if standby == "" {
		standby = defaultStandbyReplicas
	}

	id := newCollectionID()
	now := m.now()

	col := driver.Collection{
		ID:                 id,
		ARN:                m.collectionARN(id),
		Name:               in.Name,
		Description:        in.Description,
		Type:               colType,
		Status:             driver.StatusActive,
		StandbyReplicas:    standby,
		KmsKeyArn:          kmsKeyArn,
		CreatedDate:        now,
		LastModifiedDate:   now,
		CollectionEndpoint: m.collectionEndpoint(id),
		DashboardEndpoint:  m.dashboardEndpoint(id),
		Tags:               copyTags(in.Tags),
	}

	m.collections.Set(id, col)

	out := copyCollection(&col)

	return &out, nil
}

// BatchGetCollection returns the requested collections by id or name. Missing
// entries are reported in the error slice rather than failing the whole call,
// matching the real API.
func (m *Mock) BatchGetCollection(
	_ context.Context, ids, names []string,
) (details []driver.Collection, errs []driver.CollectionError, err error) {
	for _, id := range ids {
		if c, ok := m.collections.Get(id); ok {
			details = append(details, copyCollection(&c))
		} else {
			errs = append(errs, driver.CollectionError{
				ID: id, ErrorCode: "NOT_FOUND", ErrorMessage: "The specified collection id is invalid.",
			})
		}
	}

	for _, name := range names {
		if c, ok := m.collectionByName(name); ok {
			details = append(details, copyCollection(c))
		} else {
			errs = append(errs, driver.CollectionError{
				Name: name, ErrorCode: "NOT_FOUND", ErrorMessage: "The specified collection name is invalid.",
			})
		}
	}

	return details, errs, nil
}

// ListCollections returns a deterministic page of collections ordered by id,
// optionally filtered by exact name and/or status.
func (m *Mock) ListCollections(
	_ context.Context, filterName, filterStatus string, page driver.Page,
) (cols []driver.Collection, nextToken string, err error) {
	stored := m.collections.SortedValues()

	filtered := make([]driver.Collection, 0, len(stored))

	for i := range stored {
		if filterName != "" && stored[i].Name != filterName {
			continue
		}

		if filterStatus != "" && stored[i].Status != filterStatus {
			continue
		}

		filtered = append(filtered, stored[i])
	}

	start, end, next := paginate(len(filtered), page)

	out := make([]driver.Collection, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyCollection(&filtered[i]))
	}

	return out, next, nil
}

// UpdateCollection applies the supplied fields, leaving omitted parameters
// unchanged. The computed id, arn, endpoints, status and createdDate are
// preserved; lastModifiedDate is bumped.
func (m *Mock) UpdateCollection(_ context.Context, in *driver.UpdateCollectionInput) (*driver.Collection, error) {
	var updated driver.Collection

	ok := m.collections.Update(in.ID, func(c driver.Collection) driver.Collection {
		if in.Description != nil {
			c.Description = *in.Description
		}

		c.LastModifiedDate = m.now()
		updated = c

		return c
	})
	if !ok {
		return nil, notFound("collection with id %q not found", in.ID)
	}

	out := copyCollection(&updated)

	return &out, nil
}

// DeleteCollection removes a collection and returns its final description with a
// DELETING status. A subsequent BatchGetCollection reports it as missing so an
// IaC delete waiter completes.
func (m *Mock) DeleteCollection(_ context.Context, id string) (*driver.Collection, error) {
	c, ok := m.collections.Get(id)
	if !ok {
		return nil, notFound("collection with id %q not found", id)
	}

	m.collections.Delete(id)

	out := copyCollection(&c)
	out.Status = driver.StatusDeleting

	return &out, nil
}

func (m *Mock) collectionNameExists(name string) bool {
	_, ok := m.collectionByName(name)

	return ok
}

func (m *Mock) collectionByName(name string) (*driver.Collection, bool) {
	vals := m.collections.SortedValues()
	for i := range vals {
		if vals[i].Name == name {
			out := vals[i]

			return &out, true
		}
	}

	return nil, false
}

// encryptionPolicyDoc is the subset of an encryption security policy document
// the collection dependency check reads.
type encryptionPolicyDoc struct {
	Rules []struct {
		ResourceType string   `json:"ResourceType"`
		Resource     []string `json:"Resource"`
	} `json:"Rules"`
	AWSOwnedKey bool   `json:"AWSOwnedKey"`
	KmsARN      string `json:"KmsARN"`
}

// resolveEncryptionKey finds an encryption security policy whose rules cover the
// named collection and returns the kmsKeyArn the collection should report
// ("auto" for an AWS-owned key, otherwise the policy's KmsARN). ok is false when
// no encryption policy matches, which the caller surfaces as a ValidationException.
func (m *Mock) resolveEncryptionKey(collectionName string) (kmsKeyArn string, ok bool) {
	policies := m.securityPolicies.SortedValues()
	for i := range policies {
		p := &policies[i]
		if p.Type != driver.SecurityPolicyEncryption {
			continue
		}

		var doc encryptionPolicyDoc
		if err := json.Unmarshal(p.Policy, &doc); err != nil {
			continue
		}

		if !encryptionCovers(&doc, collectionName) {
			continue
		}

		if doc.AWSOwnedKey || doc.KmsARN == "" {
			return awsOwnedKeyMarker, true
		}

		return doc.KmsARN, true
	}

	return "", false
}

// encryptionCovers reports whether an encryption policy document has a
// collection rule whose resource pattern matches collectionName.
func encryptionCovers(doc *encryptionPolicyDoc, collectionName string) bool {
	for _, rule := range doc.Rules {
		if rule.ResourceType != "collection" {
			continue
		}

		for _, res := range rule.Resource {
			if resourceMatches(res, collectionName) {
				return true
			}
		}
	}

	return false
}

// resourceMatches matches a "collection/<pattern>" resource against a collection
// name, honoring a single trailing "*" wildcard (as OpenSearch Serverless policy
// resources use, e.g. "collection/*" or "collection/logs-*").
func resourceMatches(resource, collectionName string) bool {
	const prefix = "collection/"

	pattern, ok := strings.CutPrefix(resource, prefix)
	if !ok {
		return false
	}

	if stem, wild := strings.CutSuffix(pattern, "*"); wild {
		return strings.HasPrefix(collectionName, stem)
	}

	return pattern == collectionName
}

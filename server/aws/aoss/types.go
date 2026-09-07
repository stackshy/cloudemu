package aoss

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// tagJSON is the wire shape of an OpenSearch Serverless tag: an object with
// lowercase key/value members.
type tagJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func tagsToWire(tags []driver.Tag) []tagJSON {
	if tags == nil {
		return nil
	}

	out := make([]tagJSON, len(tags))
	for i, t := range tags {
		out[i] = tagJSON{Key: t.Key, Value: t.Value}
	}

	return out
}

func tagsFromWire(tags []tagJSON) []driver.Tag {
	if tags == nil {
		return nil
	}

	out := make([]driver.Tag, len(tags))
	for i, t := range tags {
		out[i] = driver.Tag{Key: t.Key, Value: t.Value}
	}

	return out
}

// epochMillis renders a driver timestamp as the epoch-milliseconds integer the
// aoss wire uses for createdDate/lastModifiedDate.
func epochMillis(t interface{ UnixMilli() int64 }) int64 {
	return t.UnixMilli()
}

// createCollectionDetailJSON is the CreateCollectionDetail shape (no endpoints;
// those appear only on the BatchGetCollection CollectionDetail).
type createCollectionDetailJSON struct {
	ARN              string `json:"arn"`
	CreatedDate      int64  `json:"createdDate"`
	Description      string `json:"description,omitempty"`
	ID               string `json:"id"`
	KmsKeyArn        string `json:"kmsKeyArn,omitempty"`
	LastModifiedDate int64  `json:"lastModifiedDate"`
	Name             string `json:"name"`
	StandbyReplicas  string `json:"standbyReplicas,omitempty"`
	Status           string `json:"status"`
	Type             string `json:"type"`
}

func toCreateCollectionDetail(c *driver.Collection) createCollectionDetailJSON {
	return createCollectionDetailJSON{
		ARN:              c.ARN,
		CreatedDate:      epochMillis(c.CreatedDate),
		Description:      c.Description,
		ID:               c.ID,
		KmsKeyArn:        c.KmsKeyArn,
		LastModifiedDate: epochMillis(c.LastModifiedDate),
		Name:             c.Name,
		StandbyReplicas:  c.StandbyReplicas,
		Status:           c.Status,
		Type:             c.Type,
	}
}

// collectionDetailJSON is the full CollectionDetail returned by BatchGetCollection.
type collectionDetailJSON struct {
	ARN                string `json:"arn"`
	CollectionEndpoint string `json:"collectionEndpoint"`
	CreatedDate        int64  `json:"createdDate"`
	DashboardEndpoint  string `json:"dashboardEndpoint"`
	Description        string `json:"description,omitempty"`
	ID                 string `json:"id"`
	KmsKeyArn          string `json:"kmsKeyArn,omitempty"`
	LastModifiedDate   int64  `json:"lastModifiedDate"`
	Name               string `json:"name"`
	StandbyReplicas    string `json:"standbyReplicas,omitempty"`
	Status             string `json:"status"`
	Type               string `json:"type"`
}

func toCollectionDetail(c *driver.Collection) collectionDetailJSON {
	return collectionDetailJSON{
		ARN:                c.ARN,
		CollectionEndpoint: c.CollectionEndpoint,
		CreatedDate:        epochMillis(c.CreatedDate),
		DashboardEndpoint:  c.DashboardEndpoint,
		Description:        c.Description,
		ID:                 c.ID,
		KmsKeyArn:          c.KmsKeyArn,
		LastModifiedDate:   epochMillis(c.LastModifiedDate),
		Name:               c.Name,
		StandbyReplicas:    c.StandbyReplicas,
		Status:             c.Status,
		Type:               c.Type,
	}
}

// updateCollectionDetailJSON is the UpdateCollectionDetail shape.
type updateCollectionDetailJSON struct {
	ARN              string `json:"arn"`
	CreatedDate      int64  `json:"createdDate"`
	Description      string `json:"description,omitempty"`
	ID               string `json:"id"`
	LastModifiedDate int64  `json:"lastModifiedDate"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Type             string `json:"type"`
}

func toUpdateCollectionDetail(c *driver.Collection) updateCollectionDetailJSON {
	return updateCollectionDetailJSON{
		ARN:              c.ARN,
		CreatedDate:      epochMillis(c.CreatedDate),
		Description:      c.Description,
		ID:               c.ID,
		LastModifiedDate: epochMillis(c.LastModifiedDate),
		Name:             c.Name,
		Status:           c.Status,
		Type:             c.Type,
	}
}

// collectionSummaryJSON is a CollectionSummary in a ListCollections response.
type collectionSummaryJSON struct {
	ARN    string `json:"arn"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// collectionErrorJSON is a CollectionErrorDetail in a BatchGetCollection response.
type collectionErrorJSON struct {
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
}

// policyDetailJSON is the SecurityPolicyDetail/AccessPolicyDetail shape. Policy
// is emitted as a JSON value (document), matching the wire contract where the
// request sends the policy as a string but the response returns it parsed.
type policyDetailJSON struct {
	CreatedDate      int64           `json:"createdDate"`
	Description      string          `json:"description,omitempty"`
	LastModifiedDate int64           `json:"lastModifiedDate"`
	Name             string          `json:"name"`
	Policy           json.RawMessage `json:"policy,omitempty"`
	PolicyVersion    string          `json:"policyVersion"`
	Type             string          `json:"type"`
}

func toPolicyDetail(p *driver.Policy) policyDetailJSON {
	return policyDetailJSON{
		CreatedDate:      epochMillis(p.CreatedDate),
		Description:      p.Description,
		LastModifiedDate: epochMillis(p.LastModifiedDate),
		Name:             p.Name,
		Policy:           p.Policy,
		PolicyVersion:    p.PolicyVersion,
		Type:             p.Type,
	}
}

// policySummaryJSON is a SecurityPolicySummary/AccessPolicySummary (no policy
// document body).
type policySummaryJSON struct {
	CreatedDate      int64  `json:"createdDate"`
	Description      string `json:"description,omitempty"`
	LastModifiedDate int64  `json:"lastModifiedDate"`
	Name             string `json:"name"`
	PolicyVersion    string `json:"policyVersion"`
	Type             string `json:"type"`
}

func toPolicySummary(p *driver.Policy) policySummaryJSON {
	return policySummaryJSON{
		CreatedDate:      epochMillis(p.CreatedDate),
		Description:      p.Description,
		LastModifiedDate: epochMillis(p.LastModifiedDate),
		Name:             p.Name,
		PolicyVersion:    p.PolicyVersion,
		Type:             p.Type,
	}
}

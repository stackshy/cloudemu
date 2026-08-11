package guardduty

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// Publishing destination statuses and types GuardDuty reports.
const (
	pubStatusPendingVerification = "PENDING_VERIFICATION"
	pubStatusPublishing          = "PUBLISHING"
	destinationTypeS3            = "S3"
)

// destData is the server-side state of a findings-publishing destination.
type destData struct {
	destinationID   string
	destinationType string
	destinationARN  string
	kmsKeyARN       string
	status          string
	tags            map[string]string
	createdAt       time.Time
	updatedAt       time.Time
}

// copyDest deep-copies a destination so a reader cannot alias its tags map.
//
//nolint:gocritic // hugeParam: taken by value to snapshot a copy of stored state.
func copyDest(d destData) destData {
	out := d
	out.tags = copyTags(d.tags)

	return out
}

// destProperties is the destinationProperties block (ARN + KMS key).
type destProperties struct {
	DestinationArn string `json:"destinationArn"`
	KmsKeyArn      string `json:"kmsKeyArn"`
}

// createPublishingRequest is the CreatePublishingDestination body.
type createPublishingRequest struct {
	DestinationType       string            `json:"destinationType"`
	DestinationProperties destProperties    `json:"destinationProperties"`
	Tags                  map[string]string `json:"tags"`
}

// updatePublishingRequest is the UpdatePublishingDestination body.
type updatePublishingRequest struct {
	DestinationProperties *destProperties `json:"destinationProperties"`
}

// CreatePublishingDestination adds an S3 publishing destination to a detector.
// The detector lock is held across the insert so a concurrent DeleteDetector
// cannot orphan it. Newly created destinations start PENDING_VERIFICATION.
func (m *Mock) CreatePublishingDestination(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req createPublishingRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	dtype := req.DestinationType
	if dtype == "" {
		dtype = destinationTypeS3
	}

	if req.DestinationProperties.DestinationArn == "" {
		return nil, badRequest("destinationProperties.destinationArn is required")
	}

	// An S3 publishing destination requires a KMS key to encrypt exported findings.
	if dtype == destinationTypeS3 && req.DestinationProperties.KmsKeyArn == "" {
		return nil, badRequest("destinationProperties.kmsKeyArn is required for an S3 destination")
	}

	now := m.now()
	destID := idgen.GenerateID("")

	dd.mu.Lock()
	dd.publishDests[destID] = destData{
		destinationID: destID, destinationType: dtype,
		destinationARN: req.DestinationProperties.DestinationArn,
		kmsKeyARN:      req.DestinationProperties.KmsKeyArn,
		status:         pubStatusPendingVerification,
		tags:           copyTags(req.Tags), createdAt: now, updatedAt: now,
	}
	dd.mu.Unlock()

	return json.Marshal(map[string]any{"destinationId": destID})
}

// DescribePublishingDestination returns a deep copy of a stored destination. A
// described destination transitions to PUBLISHING, mirroring real GuardDuty
// verification completing.
func (m *Mock) DescribePublishingDestination(_ context.Context, detectorID, destinationID string) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.Lock()

	stored, ok := dd.publishDests[destinationID]
	if !ok {
		dd.mu.Unlock()

		return nil, badRequest("publishing destination not found: %s", destinationID)
	}

	if stored.status == pubStatusPendingVerification {
		stored.status = pubStatusPublishing
		dd.publishDests[destinationID] = stored
	}

	d := copyDest(stored)
	dd.mu.Unlock()

	out := map[string]any{
		"destinationId":   d.destinationID,
		"destinationType": d.destinationType,
		"status":          d.status,
		"destinationProperties": map[string]any{
			"destinationArn": d.destinationARN,
			"kmsKeyArn":      d.kmsKeyARN,
		},
	}

	if len(d.tags) > 0 {
		out["tags"] = d.tags
	}

	return json.Marshal(out)
}

// UpdatePublishingDestination patches a destination's properties.
func (m *Mock) UpdatePublishingDestination(
	_ context.Context, detectorID, destinationID string, body json.RawMessage,
) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req updatePublishingRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	stored, ok := dd.publishDests[destinationID]
	if !ok {
		return nil, badRequest("publishing destination not found: %s", destinationID)
	}

	if req.DestinationProperties != nil {
		if req.DestinationProperties.DestinationArn != "" {
			stored.destinationARN = req.DestinationProperties.DestinationArn
		}

		if req.DestinationProperties.KmsKeyArn != "" {
			stored.kmsKeyARN = req.DestinationProperties.KmsKeyArn
		}
	}

	stored.updatedAt = m.now()
	dd.publishDests[destinationID] = stored

	return json.Marshal(map[string]any{})
}

// DeletePublishingDestination removes a destination from a detector.
func (m *Mock) DeletePublishingDestination(_ context.Context, detectorID, destinationID string) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, ok := dd.publishDests[destinationID]; !ok {
		return nil, badRequest("publishing destination not found: %s", destinationID)
	}

	delete(dd.publishDests, destinationID)

	return json.Marshal(map[string]any{})
}

// ListPublishingDestinations lists a detector's destinations, sorted by ID.
func (m *Mock) ListPublishingDestinations(_ context.Context, detectorID string, page driver.Page) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()

	ids := make([]string, 0, len(dd.publishDests))
	for id := range dd.publishDests {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	pageIDs, next, perr := paginateIDs(ids, page)
	if perr != nil {
		dd.mu.RUnlock()

		return nil, perr
	}

	dests := make([]map[string]any, 0, len(pageIDs))

	for _, id := range pageIDs {
		d := dd.publishDests[id]
		dests = append(dests, map[string]any{
			"destinationId":   d.destinationID,
			"destinationType": d.destinationType,
			"status":          d.status,
		})
	}
	dd.mu.RUnlock()

	return json.Marshal(withNextToken(map[string]any{"destinations": dests}, next))
}

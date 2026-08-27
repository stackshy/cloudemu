package guardduty

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// guarddutySnapshot is the full serialized state of the Amazon GuardDuty mock.
// The detectors store holds an unexported *detectorData whose entire payload
// (the detector plus its nested member/invitation/publishing/finding maps and
// its admin, org-config and malware settings) lives in unexported fields, so it
// is promoted to an exported form keyed by detector id. The malwarePlans and
// malwareScans stores hold unexported value types, promoted likewise; orgAdmins
// holds plain bools and round-trips through the generic memstore helper. The
// per-detector mutex, createMu, and the wired opts are not serialized.
type guarddutySnapshot struct {
	Detectors    map[string]*detectorSnapshot   `json:"detectors,omitempty"`
	OrgAdmins    json.RawMessage                `json:"orgAdmins,omitempty"`
	MalwarePlans map[string]malwarePlanSnapshot `json:"malwarePlans,omitempty"`
	MalwareScans map[string]malwareScanSnapshot `json:"malwareScans,omitempty"`
}

// detectorSnapshot mirrors detectorData. The maps of exported driver types are
// captured directly; the maps of unexported types and the admin/org/malware
// sub-state are promoted.
type detectorSnapshot struct {
	Detector        driver.Detector                    `json:"detector"`
	IPSets          map[string]driver.IPSet            `json:"ipSets,omitempty"`
	ThreatIS        map[string]driver.ThreatIntelSet   `json:"threatIS,omitempty"`
	ThreatES        map[string]driver.ThreatEntitySet  `json:"threatES,omitempty"`
	TrustES         map[string]driver.TrustedEntitySet `json:"trustES,omitempty"`
	Filters         map[string]driver.Filter           `json:"filters,omitempty"`
	Members         map[string]memberSnapshot          `json:"members,omitempty"`
	Invites         map[string]invitationSnapshot      `json:"invites,omitempty"`
	Admin           *adminSnapshot                     `json:"admin,omitempty"`
	OrgConfig       orgConfigSnapshot                  `json:"orgConfig"`
	PublishDests    map[string]destSnapshot            `json:"publishDests,omitempty"`
	Findings        map[string]findingSnapshot         `json:"findings,omitempty"`
	MalwareSettings malwareSettingsSnapshot            `json:"malwareSettings"`
}

type memberSnapshot struct {
	AccountID          string    `json:"accountId"`
	Email              string    `json:"email,omitempty"`
	RelationshipStatus string    `json:"relationshipStatus,omitempty"`
	InvitedAt          time.Time `json:"invitedAt,omitempty"`
	UpdatedAt          time.Time `json:"updatedAt,omitempty"`
}

type invitationSnapshot struct {
	InviterAccountID string    `json:"inviterAccountId"`
	InvitationID     string    `json:"invitationId,omitempty"`
	InvitedAt        time.Time `json:"invitedAt,omitempty"`
	Status           string    `json:"status,omitempty"`
}

type adminSnapshot struct {
	AccountID    string    `json:"accountId"`
	InvitationID string    `json:"invitationId,omitempty"`
	InvitedAt    time.Time `json:"invitedAt,omitempty"`
	Status       string    `json:"status,omitempty"`
}

type orgConfigSnapshot struct {
	AutoEnable        *bool             `json:"autoEnable,omitempty"`
	AutoEnableMembers string            `json:"autoEnableMembers,omitempty"`
	Features          []json.RawMessage `json:"features,omitempty"`
	DataSources       json.RawMessage   `json:"dataSources,omitempty"`
}

type destSnapshot struct {
	DestinationID   string            `json:"destinationId"`
	DestinationType string            `json:"destinationType,omitempty"`
	DestinationARN  string            `json:"destinationArn,omitempty"`
	KmsKeyARN       string            `json:"kmsKeyArn,omitempty"`
	Status          string            `json:"status,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	CreatedAt       time.Time         `json:"createdAt,omitempty"`
	UpdatedAt       time.Time         `json:"updatedAt,omitempty"`
}

type findingSnapshot struct {
	ID             string    `json:"id"`
	FindingType    string    `json:"findingType,omitempty"`
	Severity       float64   `json:"severity,omitempty"`
	Confidence     float64   `json:"confidence,omitempty"`
	Title          string    `json:"title,omitempty"`
	Description    string    `json:"description,omitempty"`
	AccountID      string    `json:"accountId,omitempty"`
	Region         string    `json:"region,omitempty"`
	ARN            string    `json:"arn,omitempty"`
	Archived       bool      `json:"archived,omitempty"`
	Count          int32     `json:"count,omitempty"`
	ResourceRole   string    `json:"resourceRole,omitempty"`
	ResourceType   string    `json:"resourceType,omitempty"`
	Feedback       string    `json:"feedback,omitempty"`
	Comment        string    `json:"comment,omitempty"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt,omitempty"`
	EventFirstSeen time.Time `json:"eventFirstSeen,omitempty"`
	EventLastSeen  time.Time `json:"eventLastSeen,omitempty"`
}

type malwareSettingsSnapshot struct {
	EbsSnapshotPreservation string          `json:"ebsSnapshotPreservation,omitempty"`
	ScanResourceCriteria    json.RawMessage `json:"scanResourceCriteria,omitempty"`
}

type malwarePlanSnapshot struct {
	ID                string            `json:"id"`
	ARN               string            `json:"arn,omitempty"`
	Role              string            `json:"role,omitempty"`
	ProtectedResource json.RawMessage   `json:"protectedResource,omitempty"`
	Actions           json.RawMessage   `json:"actions,omitempty"`
	Status            string            `json:"status,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
	CreatedAt         time.Time         `json:"createdAt,omitempty"`
}

type malwareScanSnapshot struct {
	ScanID       string    `json:"scanId"`
	ResourceARN  string    `json:"resourceArn,omitempty"`
	ResourceType string    `json:"resourceType,omitempty"`
	Status       string    `json:"status,omitempty"`
	Result       string    `json:"result,omitempty"`
	ScanType     string    `json:"scanType,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	CompletedAt  time.Time `json:"completedAt,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// GuardDuty holds resource metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := guarddutySnapshot{}

	if m.detectors.Len() > 0 {
		snap.Detectors = make(map[string]*detectorSnapshot, m.detectors.Len())

		for id, dd := range m.detectors.All() {
			dd.mu.RLock()
			snap.Detectors[id] = snapshotDetector(dd)
			dd.mu.RUnlock()
		}
	}

	admins, err := m.orgAdmins.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("guardduty: snapshot orgAdmins: %w", err)
	}

	snap.OrgAdmins = admins

	snap.MalwarePlans = snapshotMalwarePlans(m.malwarePlans.All())
	snap.MalwareScans = snapshotMalwareScans(m.malwareScans.All())

	return json.Marshal(snap)
}

func snapshotDetector(dd *detectorData) *detectorSnapshot {
	ds := &detectorSnapshot{
		Detector: dd.detector, IPSets: dd.ipSets, ThreatIS: dd.threatIS,
		ThreatES: dd.threatES, TrustES: dd.trustES, Filters: dd.filters,
		OrgConfig: orgConfigSnapshot{
			AutoEnable: dd.orgConfig.autoEnable, AutoEnableMembers: dd.orgConfig.autoEnableMembers,
			Features: dd.orgConfig.features, DataSources: dd.orgConfig.dataSources,
		},
		MalwareSettings: malwareSettingsSnapshot{
			EbsSnapshotPreservation: dd.malwareSettings.ebsSnapshotPreservation,
			ScanResourceCriteria:    dd.malwareSettings.scanResourceCriteria,
		},
	}

	if len(dd.members) > 0 {
		ds.Members = make(map[string]memberSnapshot, len(dd.members))

		for k, v := range dd.members {
			ds.Members[k] = memberSnapshot{
				AccountID: v.accountID, Email: v.email, RelationshipStatus: v.relationshipStatus,
				InvitedAt: v.invitedAt, UpdatedAt: v.updatedAt,
			}
		}
	}

	if len(dd.invites) > 0 {
		ds.Invites = make(map[string]invitationSnapshot, len(dd.invites))

		for k, v := range dd.invites {
			ds.Invites[k] = invitationSnapshot{
				InviterAccountID: v.inviterAccountID, InvitationID: v.invitationID,
				InvitedAt: v.invitedAt, Status: v.status,
			}
		}
	}

	if dd.admin != nil {
		ds.Admin = &adminSnapshot{
			AccountID: dd.admin.accountID, InvitationID: dd.admin.invitationID,
			InvitedAt: dd.admin.invitedAt, Status: dd.admin.status,
		}
	}

	if len(dd.publishDests) > 0 {
		ds.PublishDests = make(map[string]destSnapshot, len(dd.publishDests))

		for k := range dd.publishDests {
			v := dd.publishDests[k]
			ds.PublishDests[k] = destSnapshot{
				DestinationID: v.destinationID, DestinationType: v.destinationType,
				DestinationARN: v.destinationARN, KmsKeyARN: v.kmsKeyARN, Status: v.status,
				Tags: v.tags, CreatedAt: v.createdAt, UpdatedAt: v.updatedAt,
			}
		}
	}

	if len(dd.findings) > 0 {
		ds.Findings = make(map[string]findingSnapshot, len(dd.findings))

		for k := range dd.findings {
			f := dd.findings[k]
			ds.Findings[k] = findingToSnapshot(&f)
		}
	}

	return ds
}

//nolint:dupl // inverse field map of findingFromSnapshot; mirrored lists are inherent.
func findingToSnapshot(v *findingData) findingSnapshot {
	return findingSnapshot{
		ID: v.id, FindingType: v.findingType, Severity: v.severity, Confidence: v.confidence,
		Title: v.title, Description: v.description, AccountID: v.accountID, Region: v.region,
		ARN: v.arn, Archived: v.archived, Count: v.count, ResourceRole: v.resourceRole,
		ResourceType: v.resourceType, Feedback: v.feedback, Comment: v.comment,
		CreatedAt: v.createdAt, UpdatedAt: v.updatedAt,
		EventFirstSeen: v.eventFirstSeen, EventLastSeen: v.eventLastSeen,
	}
}

func snapshotMalwarePlans(all map[string]malwarePlanData) map[string]malwarePlanSnapshot {
	if len(all) == 0 {
		return nil
	}

	out := make(map[string]malwarePlanSnapshot, len(all))

	for id := range all {
		p := all[id]
		out[id] = malwarePlanSnapshot{
			ID: p.id, ARN: p.arn, Role: p.role, ProtectedResource: p.protectedResource,
			Actions: p.actions, Status: p.status, Tags: p.tags, CreatedAt: p.createdAt,
		}
	}

	return out
}

func snapshotMalwareScans(all map[string]malwareScanData) map[string]malwareScanSnapshot {
	if len(all) == 0 {
		return nil
	}

	out := make(map[string]malwareScanSnapshot, len(all))

	for id := range all {
		s := all[id]
		out[id] = malwareScanSnapshot{
			ScanID: s.scanID, ResourceARN: s.resourceARN, ResourceType: s.resourceType,
			Status: s.status, Result: s.result, ScanType: s.scanType,
			StartedAt: s.startedAt, CompletedAt: s.completedAt,
		}
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: every
// detector id, member/invitation/finding/publishing-destination key, and
// malware plan/scan id is preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap guarddutySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("guardduty: parse snapshot: %w", err)
	}

	for id, ds := range snap.Detectors {
		m.detectors.Set(id, restoreDetector(ds))
	}

	if len(snap.OrgAdmins) > 0 {
		if err := m.orgAdmins.LoadSnapshot(snap.OrgAdmins); err != nil {
			return fmt.Errorf("guardduty: restore orgAdmins: %w", err)
		}
	}

	for id := range snap.MalwarePlans {
		p := snap.MalwarePlans[id]
		m.malwarePlans.Set(id, malwarePlanData{
			id: p.ID, arn: p.ARN, role: p.Role, protectedResource: p.ProtectedResource,
			actions: p.Actions, status: p.Status, tags: p.Tags, createdAt: p.CreatedAt,
		})
	}

	for id := range snap.MalwareScans {
		s := snap.MalwareScans[id]
		m.malwareScans.Set(id, malwareScanData{
			scanID: s.ScanID, resourceARN: s.ResourceARN, resourceType: s.ResourceType,
			status: s.Status, result: s.Result, scanType: s.ScanType,
			startedAt: s.StartedAt, completedAt: s.CompletedAt,
		})
	}

	return nil
}

func restoreDetector(ds *detectorSnapshot) *detectorData {
	dd := &detectorData{
		detector: ds.Detector, ipSets: ds.IPSets, threatIS: ds.ThreatIS,
		threatES: ds.ThreatES, trustES: ds.TrustES, filters: ds.Filters,
		members: map[string]memberData{}, invites: map[string]invitationData{},
		publishDests: map[string]destData{}, findings: map[string]findingData{},
		orgConfig: orgConfigData{
			autoEnable: ds.OrgConfig.AutoEnable, autoEnableMembers: ds.OrgConfig.AutoEnableMembers,
			features: ds.OrgConfig.Features, dataSources: ds.OrgConfig.DataSources,
		},
		malwareSettings: malwareScanSettings{
			ebsSnapshotPreservation: ds.MalwareSettings.EbsSnapshotPreservation,
			scanResourceCriteria:    ds.MalwareSettings.ScanResourceCriteria,
		},
	}

	for k, v := range ds.Members {
		dd.members[k] = memberData{
			accountID: v.AccountID, email: v.Email, relationshipStatus: v.RelationshipStatus,
			invitedAt: v.InvitedAt, updatedAt: v.UpdatedAt,
		}
	}

	for k, v := range ds.Invites {
		dd.invites[k] = invitationData{
			inviterAccountID: v.InviterAccountID, invitationID: v.InvitationID,
			invitedAt: v.InvitedAt, status: v.Status,
		}
	}

	if ds.Admin != nil {
		dd.admin = &adminLink{
			accountID: ds.Admin.AccountID, invitationID: ds.Admin.InvitationID,
			invitedAt: ds.Admin.InvitedAt, status: ds.Admin.Status,
		}
	}

	for k := range ds.PublishDests {
		v := ds.PublishDests[k]
		dd.publishDests[k] = destData{
			destinationID: v.DestinationID, destinationType: v.DestinationType,
			destinationARN: v.DestinationARN, kmsKeyARN: v.KmsKeyARN, status: v.Status,
			tags: v.Tags, createdAt: v.CreatedAt, updatedAt: v.UpdatedAt,
		}
	}

	for k := range ds.Findings {
		f := ds.Findings[k]
		dd.findings[k] = findingFromSnapshot(&f)
	}

	return dd
}

//nolint:dupl // inverse field map of findingToSnapshot; mirrored lists are inherent.
func findingFromSnapshot(v *findingSnapshot) findingData {
	return findingData{
		id: v.ID, findingType: v.FindingType, severity: v.Severity, confidence: v.Confidence,
		title: v.Title, description: v.Description, accountID: v.AccountID, region: v.Region,
		arn: v.ARN, archived: v.Archived, count: v.Count, resourceRole: v.ResourceRole,
		resourceType: v.ResourceType, feedback: v.Feedback, comment: v.Comment,
		createdAt: v.CreatedAt, updatedAt: v.UpdatedAt,
		eventFirstSeen: v.EventFirstSeen, eventLastSeen: v.EventLastSeen,
	}
}

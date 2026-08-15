// Package driver defines the interface and types for Amazon GuardDuty
// implementations. It models detectors and their child resources (IP sets,
// threat-intel sets, threat-entity sets, trusted-entity sets, and filters) as
// plain Go types, plus the membership, findings, organization, malware, and
// publishing operations required for full aws-sdk-go-v2 wire parity.
//
// GuardDuty uses the REST-JSON (awsRestjson1) protocol with path + HTTP-method
// routing and no version-path prefix. Operations with large or open-ended
// request/response shapes (members, findings, org config, malware protection,
// publishing destinations, coverage, usage, and resource tags) carry their
// bodies as json.RawMessage built to the SDK wire shape, rather than modeling
// every nested field as a Go type.
package driver

import (
	"context"
	"encoding/json"
)

// GuardDuty is the interface a GuardDuty backend implements. It carries one
// method per GuardDuty operation (87 total) so a handler can route the full
// API surface: detectors, IP sets, threat-intel/entity sets, trusted-entity
// sets, filters, members, invitations, organization config, publishing
// destinations, findings, coverage, usage, malware protection, and tags.
//
//nolint:interfacebloat // GuardDuty exposes 87 operations; full parity requires them all.
type GuardDuty interface {
	// Detectors.
	CreateDetector(ctx context.Context, in CreateDetectorInput) (*Detector, error)
	GetDetector(ctx context.Context, detectorID string) (*Detector, error)
	UpdateDetector(ctx context.Context, in UpdateDetectorInput) error
	DeleteDetector(ctx context.Context, detectorID string) error
	ListDetectors(ctx context.Context, page Page) (ids []string, next string, err error)

	// IP sets (per-detector).
	CreateIPSet(ctx context.Context, in CreateIPSetInput) (id string, err error)
	GetIPSet(ctx context.Context, detectorID, ipSetID string) (*IPSet, error)
	UpdateIPSet(ctx context.Context, in UpdateIPSetInput) error
	DeleteIPSet(ctx context.Context, detectorID, ipSetID string) error
	ListIPSets(ctx context.Context, detectorID string, page Page) (ids []string, next string, err error)

	// Threat-intel sets (per-detector).
	CreateThreatIntelSet(ctx context.Context, in CreateThreatIntelSetInput) (id string, err error)
	GetThreatIntelSet(ctx context.Context, detectorID, setID string) (*ThreatIntelSet, error)
	UpdateThreatIntelSet(ctx context.Context, in UpdateThreatIntelSetInput) error
	DeleteThreatIntelSet(ctx context.Context, detectorID, setID string) error
	ListThreatIntelSets(ctx context.Context, detectorID string, page Page) (ids []string, next string, err error)

	// Threat-entity sets (per-detector).
	CreateThreatEntitySet(ctx context.Context, in CreateThreatEntitySetInput) (id string, err error)
	GetThreatEntitySet(ctx context.Context, detectorID, setID string) (*ThreatEntitySet, error)
	UpdateThreatEntitySet(ctx context.Context, in UpdateThreatEntitySetInput) error
	DeleteThreatEntitySet(ctx context.Context, detectorID, setID string) error
	ListThreatEntitySets(ctx context.Context, detectorID string, page Page) (ids []string, next string, err error)

	// Trusted-entity sets (per-detector).
	CreateTrustedEntitySet(ctx context.Context, in CreateTrustedEntitySetInput) (id string, err error)
	GetTrustedEntitySet(ctx context.Context, detectorID, setID string) (*TrustedEntitySet, error)
	UpdateTrustedEntitySet(ctx context.Context, in UpdateTrustedEntitySetInput) error
	DeleteTrustedEntitySet(ctx context.Context, detectorID, setID string) error
	ListTrustedEntitySets(ctx context.Context, detectorID string, page Page) (ids []string, next string, err error)

	// Filters (per-detector).
	CreateFilter(ctx context.Context, in CreateFilterInput) (name string, err error)
	GetFilter(ctx context.Context, detectorID, filterName string) (*Filter, error)
	UpdateFilter(ctx context.Context, in UpdateFilterInput) (name string, err error)
	DeleteFilter(ctx context.Context, detectorID, filterName string) error
	ListFilters(ctx context.Context, detectorID string, page Page) (names []string, next string, err error)

	// Members & administrator/master. Bodies are carried as raw JSON
	// so the wire layer can route them before the backend models them.
	CreateMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	DeleteMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	ListMembers(ctx context.Context, detectorID string, page Page) (json.RawMessage, error)
	InviteMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	DisassociateMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	StartMonitoringMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	StopMonitoringMembers(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetMemberDetectors(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	UpdateMemberDetectors(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	AcceptAdministratorInvitation(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetAdministratorAccount(ctx context.Context, detectorID string) (json.RawMessage, error)
	DisassociateFromAdministratorAccount(ctx context.Context, detectorID string) (json.RawMessage, error)
	AcceptInvitation(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetMasterAccount(ctx context.Context, detectorID string) (json.RawMessage, error)
	DisassociateFromMasterAccount(ctx context.Context, detectorID string) (json.RawMessage, error)

	// Invitations.
	DeclineInvitations(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	DeleteInvitations(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	ListInvitations(ctx context.Context, page Page) (json.RawMessage, error)
	GetInvitationsCount(ctx context.Context) (json.RawMessage, error)

	// Findings.
	ArchiveFindings(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	UnarchiveFindings(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	CreateSampleFindings(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetFindings(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetFindingsStatistics(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	ListFindings(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	UpdateFindingsFeedback(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)

	// Organization.
	EnableOrganizationAdminAccount(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	DisableOrganizationAdminAccount(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	ListOrganizationAdminAccounts(ctx context.Context, page Page) (json.RawMessage, error)
	DescribeOrganizationConfiguration(ctx context.Context, detectorID string, page Page) (json.RawMessage, error)
	UpdateOrganizationConfiguration(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetOrganizationStatistics(ctx context.Context) (json.RawMessage, error)

	// Publishing destinations.
	CreatePublishingDestination(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	DescribePublishingDestination(ctx context.Context, detectorID, destinationID string) (json.RawMessage, error)
	UpdatePublishingDestination(ctx context.Context, detectorID, destinationID string, body json.RawMessage) (json.RawMessage, error)
	DeletePublishingDestination(ctx context.Context, detectorID, destinationID string) (json.RawMessage, error)
	ListPublishingDestinations(ctx context.Context, detectorID string, page Page) (json.RawMessage, error)

	// Malware protection & scans.
	CreateMalwareProtectionPlan(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	GetMalwareProtectionPlan(ctx context.Context, planID string) (json.RawMessage, error)
	UpdateMalwareProtectionPlan(ctx context.Context, planID string, body json.RawMessage) (json.RawMessage, error)
	DeleteMalwareProtectionPlan(ctx context.Context, planID string) (json.RawMessage, error)
	ListMalwareProtectionPlans(ctx context.Context, page Page) (json.RawMessage, error)
	DescribeMalwareScans(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetMalwareScan(ctx context.Context, scanID string) (json.RawMessage, error)
	ListMalwareScans(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	StartMalwareScan(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	SendObjectMalwareScan(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	GetMalwareScanSettings(ctx context.Context, detectorID string) (json.RawMessage, error)
	UpdateMalwareScanSettings(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)

	// Coverage, usage, and free-trial.
	ListCoverage(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetCoverageStatistics(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetUsageStatistics(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)
	GetRemainingFreeTrialDays(ctx context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error)

	// Resource tags.
	ListTagsForResource(ctx context.Context, resourceARN string) (json.RawMessage, error)
	TagResource(ctx context.Context, resourceARN string, body json.RawMessage) (json.RawMessage, error)
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) (json.RawMessage, error)
}

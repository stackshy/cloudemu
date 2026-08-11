// Package guardduty provides an in-memory mock implementation of Amazon
// GuardDuty: detectors that are provisioned immediately ENABLED, and their
// child resources — IP sets, threat-intel sets, threat-entity sets,
// trusted-entity sets, and filters — each keyed under their owning detector.
//
// It implements the full GuardDuty API surface end to end: detectors and their
// child resources (IP sets, threat-intel/entity sets, trusted-entity sets,
// filters), members and invitations, findings, organization configuration,
// publishing destinations, malware protection plans and scans, coverage, usage,
// and resource tags. Account-level resources (org admins, malware protection
// plans, malware scans) live in Mock-level stores; per-detector resources live
// under their owning detector's lock.
package guardduty

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// Compile-time check that Mock implements driver.GuardDuty.
var _ driver.GuardDuty = (*Mock)(nil)

const (
	// defaultMaxResults caps a page when the caller requests none. GuardDuty's
	// list operations default (and cap) at 50.
	defaultMaxResults = 50
	// serviceRole is the synthesized service-linked role a detector reports.
	serviceRoleName = "AWSServiceRoleForAmazonGuardDuty"
)

// detectorData is the full server-side state of a detector plus its own lock.
// Child resources live in maps guarded by the same lock so a create's
// parent-existence check and the child insert are atomic against a concurrent
// DeleteDetector, which cannot then orphan a child.
type detectorData struct {
	detector driver.Detector
	ipSets   map[string]driver.IPSet
	threatIS map[string]driver.ThreatIntelSet
	threatES map[string]driver.ThreatEntitySet
	trustES  map[string]driver.TrustedEntitySet
	filters  map[string]driver.Filter
	// members maps a member accountId to the account this detector administers.
	members map[string]memberData
	// invites maps an inviter (administrator) accountId to a pending invitation
	// this detector's account has received.
	invites map[string]invitationData
	// admin is the accepted administrator/master linkage, nil when none.
	admin *adminLink
	// orgConfig is this detector's organization auto-enable configuration.
	orgConfig orgConfigData
	// publishDests maps a server-minted destinationId to a publishing destination.
	publishDests map[string]destData
	// findings maps a server-minted findingId to a stored finding under this detector.
	findings map[string]findingData
	// malwareSettings is this detector's malware-scan configuration
	// (ScanResourceCriteria + EbsSnapshotPreservation), carried verbatim as raw
	// JSON because the emulator does not interpret the criteria.
	malwareSettings malwareScanSettings
	mu              sync.RWMutex
}

// Mock is an in-memory implementation of Amazon GuardDuty.
type Mock struct {
	detectors *memstore.Store[*detectorData]
	// orgAdmins is the account-wide set of delegated GuardDuty administrator
	// account IDs, independent of any single detector.
	orgAdmins *memstore.Store[bool]
	// malwarePlans is the account-wide set of Malware Protection plans. Plans are
	// an account-level resource, not a per-detector child, so they live in their
	// own Mock-level store keyed by a server-minted plan ID.
	malwarePlans *memstore.Store[malwarePlanData]
	// malwareScans is the account-wide set of on-demand malware scans, keyed by a
	// server-minted scan ID. StartMalwareScan is not scoped to a detector, so its
	// results live at the Mock level too.
	malwareScans *memstore.Store[malwareScanData]
	// createMu serializes CreateDetector so the "one detector per account" cap is
	// enforced atomically: SetIfAbsent alone can't guard a count invariant
	// because each detector gets a distinct server-minted ID.
	createMu sync.Mutex
	opts     *config.Options
}

// New creates a new GuardDuty mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		detectors:    memstore.New[*detectorData](),
		orgAdmins:    memstore.New[bool](),
		malwarePlans: memstore.New[malwarePlanData](),
		malwareScans: memstore.New[malwareScanData](),
		opts:         opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// serviceRoleARN returns the service-linked-role ARN a detector reports as its
// ServiceRole, matching real GuardDuty's format.
func (m *Mock) serviceRoleARN() string {
	return idgen.AWSARN("iam", "", m.opts.AccountID,
		"role/aws-service-role/guardduty.amazonaws.com/"+serviceRoleName)
}

// newDetectorID mints a 32-hex-character detector ID, the shape real GuardDuty
// uses.
func (*Mock) newDetectorID() string {
	return idgen.GenerateID("") + idgen.GenerateID("")
}

// getDetector resolves a detector by ID. Every operation that takes a detectorId
// models only BadRequestException, so both an empty and an unknown ID return
// that (real GuardDuty rejects an unknown detectorId with BadRequestException,
// not ResourceNotFoundException).
func (m *Mock) getDetector(detectorID string) (*detectorData, error) {
	if detectorID == "" {
		return nil, badRequest("detectorId is required")
	}

	dd, ok := m.detectors.Get(detectorID)
	if !ok {
		return nil, badRequest("The request is rejected because the input detectorId is not owned by the current account: %s", detectorID)
	}

	return dd, nil
}

// setStatus maps an Activate flag to a set status.
func setStatus(activate bool) string {
	if activate {
		return driver.SetStatusActive
	}

	return driver.SetStatusInactive
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}

	return append(json.RawMessage(nil), in...)
}

func copyRawSlice(in []json.RawMessage) []json.RawMessage {
	if in == nil {
		return nil
	}

	out := make([]json.RawMessage, len(in))
	for i := range in {
		out[i] = copyRaw(in[i])
	}

	return out
}

// paginateIDs returns a deterministic page of a sorted string slice, honoring an
// opaque numeric offset token. A corrupt or out-of-range token yields a
// BadRequestException so a client learns its token was bad.
func paginateIDs(ids []string, page driver.Page) (out []string, next string, err error) {
	return paginateSlice(ids, page)
}

// listChildIDs returns a sorted, paginated page of the keys of a per-detector
// child-resource map, collected under the detector's read lock. It is the shared
// body of the List operations for the detector's child resources (IP sets,
// threat-intel/entity sets, trusted-entity sets, filters).
func listChildIDs[V any](dd *detectorData, m map[string]V, page driver.Page) (ids []string, next string, err error) {
	dd.mu.RLock()

	all := make([]string, 0, len(m))
	for id := range m {
		all = append(all, id)
	}
	dd.mu.RUnlock()

	sort.Strings(all)

	return paginateIDs(all, page)
}

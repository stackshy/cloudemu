package guardduty

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// Member relationship statuses GuardDuty transitions a member account through.
// A disassociated member reports DISABLED, the same value StopMonitoring uses.
const (
	relCreated  = "CREATED"
	relInvited  = "INVITED"
	relEnabled  = "ENABLED"
	relDisabled = "DISABLED"
)

// msgNoMemberAccount is the unprocessed-account reason reported when a requested
// member account is not registered under the detector.
const msgNoMemberAccount = "member account does not exist"

// memberData is the server-side state of one member account this detector
// administers.
type memberData struct {
	accountID          string
	email              string
	relationshipStatus string
	invitedAt          time.Time
	updatedAt          time.Time
}

// memberTimestamp renders a stored timestamp as the string GuardDuty reports on
// the wire for a member's invitedAt/updatedAt fields.
func memberTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(isoTimestamp)
}

// isoTimestamp is the timestamp layout the emulator reports for member and
// invitation string timestamps.
const isoTimestamp = "2006-01-02T15:04:05.000Z"

// memberToWire renders a member for a Get/List response.
//
//nolint:gocritic // hugeParam: taken by value to match the copy semantics of stored state.
func memberToWire(detectorID, adminAccountID string, md memberData) map[string]any {
	out := map[string]any{
		"accountId":          md.accountID,
		"masterId":           adminAccountID,
		"administratorId":    adminAccountID,
		"detectorId":         detectorID,
		"relationshipStatus": md.relationshipStatus,
		"updatedAt":          memberTimestamp(md.updatedAt),
	}

	if md.email != "" {
		out["email"] = md.email
	}

	if !md.invitedAt.IsZero() {
		out["invitedAt"] = memberTimestamp(md.invitedAt)
	}

	return out
}

// unprocessed builds an UnprocessedAccount wire entry.
func unprocessed(accountID, result string) map[string]any {
	return map[string]any{"accountId": accountID, "result": result}
}

// accountDetail is one entry of the CreateMembers accountDetails list.
type accountDetail struct {
	AccountID string `json:"accountId"`
	Email     string `json:"email"`
}

// createMembersRequest is the CreateMembers request body.
type createMembersRequest struct {
	AccountDetails []accountDetail `json:"accountDetails"`
}

// accountIDsRequest is the shared body for the ops that take a list of member
// account IDs (GetMembers, DeleteMembers, disassociate/start/stop, member
// detectors).
type accountIDsRequest struct {
	AccountIDs []string `json:"accountIds"`
}

// inviteMembersRequest is the InviteMembers request body.
type inviteMembersRequest struct {
	AccountIDs []string `json:"accountIds"`
}

// CreateMembers associates member accounts with this administrator detector.
// Each account is created idempotently; a re-created account is left untouched
// and reported as unprocessed, matching real GuardDuty.
func (m *Mock) CreateMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req createMembersRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	now := m.now()

	dd.mu.Lock()
	unproc := make([]map[string]any, 0)

	for _, ad := range req.AccountDetails {
		if _, exists := dd.members[ad.AccountID]; exists {
			unproc = append(unproc, unprocessed(ad.AccountID, "member account already exists"))

			continue
		}

		dd.members[ad.AccountID] = memberData{
			accountID: ad.AccountID, email: ad.Email,
			relationshipStatus: relCreated, updatedAt: now,
		}
	}
	dd.mu.Unlock()

	return marshalUnprocessed(unproc)
}

// GetMembers returns the requested members plus an unprocessed list for any
// account IDs that are not members.
func (m *Mock) GetMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req accountIDsRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	admin := m.opts.AccountID

	dd.mu.RLock()
	members := make([]map[string]any, 0, len(req.AccountIDs))
	unproc := make([]map[string]any, 0)

	for _, id := range req.AccountIDs {
		md, ok := dd.members[id]
		if !ok {
			unproc = append(unproc, unprocessed(id, msgNoMemberAccount))

			continue
		}

		members = append(members, memberToWire(detectorID, admin, md))
	}
	dd.mu.RUnlock()

	return json.Marshal(map[string]any{"members": members, "unprocessedAccounts": unproc})
}

// ListMembers lists all members of this detector, sorted by account ID.
func (m *Mock) ListMembers(_ context.Context, detectorID string, page driver.Page) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	admin := m.opts.AccountID

	dd.mu.RLock()

	ids := make([]string, 0, len(dd.members))
	for id := range dd.members {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	pageIDs, next, perr := paginateIDs(ids, page)
	if perr != nil {
		dd.mu.RUnlock()

		return nil, perr
	}

	members := make([]map[string]any, 0, len(pageIDs))
	for _, id := range pageIDs {
		members = append(members, memberToWire(detectorID, admin, dd.members[id]))
	}
	dd.mu.RUnlock()

	return json.Marshal(withNextToken(map[string]any{"members": members}, next))
}

// DeleteMembers removes member accounts. Missing accounts are reported as
// unprocessed rather than failing the whole call.
func (m *Mock) DeleteMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	return m.mutateMembers(detectorID, body, func(dd *detectorData, id string, _ time.Time) (bool, string) {
		if _, ok := dd.members[id]; !ok {
			return false, msgNoMemberAccount
		}

		delete(dd.members, id)

		return true, ""
	})
}

// InviteMembers moves CREATED members to INVITED on the administrator's detector.
// Missing accounts are returned as unprocessed.
//
// Delivering the invitation to the invited account's own detector is inherently
// cross-account; cloudemu emulates a single account, so the invited account's
// detector does not exist here and no pending invitation is delivered. The
// administrator-side status transition above is the observable effect. The
// invitee side (AcceptInvitation consuming a pending invitation) is exercised in
// tests via the AddPendingInvitationForTest seam.
func (m *Mock) InviteMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req inviteMembersRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	now := m.now()

	dd.mu.Lock()
	unproc := make([]map[string]any, 0)

	for _, id := range req.AccountIDs {
		md, ok := dd.members[id]
		if !ok {
			unproc = append(unproc, unprocessed(id, msgNoMemberAccount))

			continue
		}

		md.relationshipStatus = relInvited
		md.invitedAt = now
		md.updatedAt = now
		dd.members[id] = md
	}
	dd.mu.Unlock()

	return marshalUnprocessed(unproc)
}

// DisassociateMembers marks members DISABLED (disassociated).
func (m *Mock) DisassociateMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	return m.transitionMembers(detectorID, body, relDisabled)
}

// StartMonitoringMembers enables monitoring, moving members to ENABLED.
func (m *Mock) StartMonitoringMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	return m.transitionMembers(detectorID, body, relEnabled)
}

// StopMonitoringMembers disables monitoring, moving members to DISABLED.
func (m *Mock) StopMonitoringMembers(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	return m.transitionMembers(detectorID, body, relDisabled)
}

// GetMemberDetectors returns a minimal member data-source configuration for each
// member; unknown accounts are unprocessed.
func (m *Mock) GetMemberDetectors(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req accountIDsRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	dd.mu.RLock()
	configs := make([]map[string]any, 0, len(req.AccountIDs))
	unproc := make([]map[string]any, 0)

	for _, id := range req.AccountIDs {
		if _, ok := dd.members[id]; !ok {
			unproc = append(unproc, unprocessed(id, msgNoMemberAccount))

			continue
		}

		configs = append(configs, map[string]any{"accountId": id})
	}
	dd.mu.RUnlock()

	return json.Marshal(map[string]any{
		"memberDataSourceConfigurations": configs,
		"unprocessedAccounts":            unproc,
	})
}

// UpdateMemberDetectors touches members' updatedAt; unknown accounts are
// unprocessed.
func (m *Mock) UpdateMemberDetectors(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	return m.mutateMembers(detectorID, body, func(dd *detectorData, id string, now time.Time) (bool, string) {
		md, ok := dd.members[id]
		if !ok {
			return false, msgNoMemberAccount
		}

		md.updatedAt = now
		dd.members[id] = md

		return true, ""
	})
}

// transitionMembers sets a new relationship status on each requested member,
// reporting non-members as unprocessed.
func (m *Mock) transitionMembers(detectorID string, body json.RawMessage, status string) (json.RawMessage, error) {
	return m.mutateMembers(detectorID, body, func(dd *detectorData, id string, now time.Time) (bool, string) {
		md, ok := dd.members[id]
		if !ok {
			return false, msgNoMemberAccount
		}

		md.relationshipStatus = status
		md.updatedAt = now
		dd.members[id] = md

		return true, ""
	})
}

// mutateMembers resolves the detector, decodes an accountIds body, applies fn to
// each account under the detector lock, and returns the unprocessed list.
func (m *Mock) mutateMembers(detectorID string, body json.RawMessage,
	fn func(dd *detectorData, id string, now time.Time) (ok bool, reason string),
) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req accountIDsRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	now := m.now()

	dd.mu.Lock()
	unproc := make([]map[string]any, 0)

	for _, id := range req.AccountIDs {
		if ok, reason := fn(dd, id, now); !ok {
			unproc = append(unproc, unprocessed(id, reason))
		}
	}
	dd.mu.Unlock()

	return marshalUnprocessed(unproc)
}

// marshalUnprocessed marshals an unprocessedAccounts-only response.
func marshalUnprocessed(unproc []map[string]any) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"unprocessedAccounts": unproc})
}

// unmarshalBody unmarshals a request body, treating an empty body as an empty
// object. A malformed body yields a BadRequestException.
func unmarshalBody(body json.RawMessage, v any) error {
	if len(body) == 0 {
		return nil
	}

	if err := json.Unmarshal(body, v); err != nil {
		return badRequest("invalid request body: %v", err)
	}

	return nil
}

// withNextToken adds a nextToken to a response map when next is non-empty.
func withNextToken(m map[string]any, next string) map[string]any {
	if next != "" {
		m["nextToken"] = next
	}

	return m
}

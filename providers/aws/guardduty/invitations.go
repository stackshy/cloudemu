package guardduty

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// adminLink is the accepted administrator/master relationship a member detector
// holds. master and administrator are the legacy and new names for this same
// concept, so both accept/get op-pairs read and write this one link.
type adminLink struct {
	accountID    string
	invitationID string
	invitedAt    time.Time
	status       string
}

// invitationData is a pending invitation this detector's account has received
// from an administrator account.
type invitationData struct {
	inviterAccountID string
	invitationID     string
	invitedAt        time.Time
	status           string
}

// acceptInvitationRequest is the AcceptInvitation request body (master naming).
type acceptInvitationRequest struct {
	MasterID     string `json:"masterId"`
	InvitationID string `json:"invitationId"`
}

// acceptAdminInvitationRequest is the AcceptAdministratorInvitation request body
// (administrator naming) — the same concept as acceptInvitationRequest.
type acceptAdminInvitationRequest struct {
	AdministratorID string `json:"administratorId"`
	InvitationID    string `json:"invitationId"`
}

// AcceptInvitation establishes the administrator link via the legacy master
// naming. It must correspond to a pending invitation.
func (m *Mock) AcceptInvitation(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	var req acceptInvitationRequest
	if err := unmarshalBody(body, &req); err != nil {
		return nil, err
	}

	return m.acceptAdmin(detectorID, req.MasterID, req.InvitationID)
}

// AcceptAdministratorInvitation establishes the administrator link via the new
// administrator naming; identical behavior to AcceptInvitation.
func (m *Mock) AcceptAdministratorInvitation(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	var req acceptAdminInvitationRequest
	if err := unmarshalBody(body, &req); err != nil {
		return nil, err
	}

	return m.acceptAdmin(detectorID, req.AdministratorID, req.InvitationID)
}

// acceptAdmin promotes a pending invitation from adminAccountID into the
// accepted administrator link. A missing or mismatched invitation is a
// BadRequestException.
func (m *Mock) acceptAdmin(detectorID, adminAccountID, invitationID string) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	if adminAccountID == "" {
		return nil, badRequest("administrator account id is required")
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	inv, ok := dd.invites[adminAccountID]
	if !ok || (invitationID != "" && inv.invitationID != invitationID) {
		return nil, badRequest("no pending invitation from account %s", adminAccountID)
	}

	dd.admin = &adminLink{
		accountID: adminAccountID, invitationID: inv.invitationID,
		invitedAt: inv.invitedAt, status: relEnabled,
	}
	delete(dd.invites, adminAccountID)

	return json.Marshal(map[string]any{})
}

// GetAdministratorAccount reads the administrator link via administrator naming.
func (m *Mock) GetAdministratorAccount(_ context.Context, detectorID string) (json.RawMessage, error) {
	return m.getAdminLink(detectorID, "administrator")
}

// GetMasterAccount reads the same administrator link via legacy master naming.
func (m *Mock) GetMasterAccount(_ context.Context, detectorID string) (json.RawMessage, error) {
	return m.getAdminLink(detectorID, "master")
}

// getAdminLink returns the accepted administrator link under the requested wire
// key (administrator|master). An empty object is returned when no link exists.
func (m *Mock) getAdminLink(detectorID, wireKey string) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	if dd.admin == nil {
		return json.Marshal(map[string]any{})
	}

	link := map[string]any{
		"accountId":          dd.admin.accountID,
		"invitationId":       dd.admin.invitationID,
		"relationshipStatus": dd.admin.status,
	}
	if !dd.admin.invitedAt.IsZero() {
		link["invitedAt"] = memberTimestamp(dd.admin.invitedAt)
	}

	return json.Marshal(map[string]any{wireKey: link})
}

// DisassociateFromAdministratorAccount clears the administrator link.
func (m *Mock) DisassociateFromAdministratorAccount(_ context.Context, detectorID string) (json.RawMessage, error) {
	return m.clearAdminLink(detectorID)
}

// DisassociateFromMasterAccount clears the same administrator link.
func (m *Mock) DisassociateFromMasterAccount(_ context.Context, detectorID string) (json.RawMessage, error) {
	return m.clearAdminLink(detectorID)
}

func (m *Mock) clearAdminLink(detectorID string) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.Lock()
	dd.admin = nil
	dd.mu.Unlock()

	return json.Marshal(map[string]any{})
}

// ListInvitations lists pending invitations across every detector, deduplicated
// and sorted by inviter account ID.
func (m *Mock) ListInvitations(_ context.Context, page driver.Page) (json.RawMessage, error) {
	byInviter := m.collectInvitations()

	ids := make([]string, 0, len(byInviter))
	for id := range byInviter {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	pageIDs, next, err := paginateIDs(ids, page)
	if err != nil {
		return nil, err
	}

	invs := make([]map[string]any, 0, len(pageIDs))
	for _, id := range pageIDs {
		inv := byInviter[id]
		entry := map[string]any{
			"accountId":          inv.inviterAccountID,
			"invitationId":       inv.invitationID,
			"relationshipStatus": inv.status,
		}
		if !inv.invitedAt.IsZero() {
			entry["invitedAt"] = memberTimestamp(inv.invitedAt)
		}

		invs = append(invs, entry)
	}

	return json.Marshal(withNextToken(map[string]any{"invitations": invs}, next))
}

// GetInvitationsCount returns the number of distinct pending invitations.
func (m *Mock) GetInvitationsCount(_ context.Context) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"invitationsCount": len(m.collectInvitations())})
}

// DeclineInvitations removes pending invitations from the given inviter account
// IDs; unknown inviters are reported as unprocessed.
func (m *Mock) DeclineInvitations(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	return m.removeInvitations(body)
}

// DeleteInvitations removes pending invitations; identical to Decline in this
// emulator (both drop the pending record).
func (m *Mock) DeleteInvitations(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	return m.removeInvitations(body)
}

// removeInvitations drops pending invitations from the requested inviter account
// IDs across every detector, reporting inviters with no pending invitation as
// unprocessed.
func (m *Mock) removeInvitations(body json.RawMessage) (json.RawMessage, error) {
	var req accountIDsRequest
	if err := unmarshalBody(body, &req); err != nil {
		return nil, err
	}

	unproc := make([]map[string]any, 0)

	for _, id := range req.AccountIDs {
		if !m.dropInvitation(id) {
			unproc = append(unproc, unprocessed(id, "no pending invitation from this account"))
		}
	}

	return marshalUnprocessed(unproc)
}

// dropInvitation removes a pending invitation from inviter across all detectors,
// reporting whether any was removed.
func (m *Mock) dropInvitation(inviter string) bool {
	removed := false

	for _, id := range m.detectors.Keys() {
		dd, ok := m.detectors.Get(id)
		if !ok {
			continue
		}

		dd.mu.Lock()
		if _, has := dd.invites[inviter]; has {
			delete(dd.invites, inviter)

			removed = true
		}
		dd.mu.Unlock()
	}

	return removed
}

// collectInvitations gathers pending invitations across all detectors keyed by
// inviter account ID (last write wins for duplicates).
func (m *Mock) collectInvitations() map[string]invitationData {
	out := map[string]invitationData{}

	for _, id := range m.detectors.Keys() {
		dd, ok := m.detectors.Get(id)
		if !ok {
			continue
		}

		dd.mu.RLock()
		for inviter, inv := range dd.invites {
			out[inviter] = inv
		}
		dd.mu.RUnlock()
	}

	return out
}

// addPendingInvitation records a pending invitation on a detector from the given
// inviter (administrator) account. Real GuardDuty creates these cross-account
// via the inviter's InviteMembers; the emulator is single-account, so this
// seam lets a caller (and tests) stage the invitee side an AcceptInvitation can
// then consume. It returns the minted invitation ID. It is unexported and used
// only by the test-only AddPendingInvitationForTest wrapper in export_test.go.
func (m *Mock) addPendingInvitation(detectorID, inviter string) (string, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return "", err
	}

	invitationID := idgen.GenerateID("")
	now := m.now()

	dd.mu.Lock()
	dd.invites[inviter] = invitationData{
		inviterAccountID: inviter, invitationID: invitationID,
		invitedAt: now, status: relInvited,
	}
	dd.mu.Unlock()

	return invitationID, nil
}

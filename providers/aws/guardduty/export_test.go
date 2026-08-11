package guardduty

// This file exposes internal seams to the guardduty_test package. Because it
// ends in _test.go it is compiled only under `go test`, so the production build
// never exposes these helpers on the Mock type.

import "github.com/stackshy/cloudemu/v2/internal/idgen"

// AddPendingInvitationForTest stages a pending invitation on a detector from the
// given inviter account so a test can exercise the invitee side of
// AcceptInvitation. Real GuardDuty creates these cross-account via the inviter's
// InviteMembers; the emulator is single-account, so this seam lets a test stage
// the invitee side an AcceptInvitation can then consume. It returns the minted
// invitation ID.
func (m *Mock) AddPendingInvitationForTest(detectorID, inviter string) (string, error) {
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

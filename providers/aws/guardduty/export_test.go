package guardduty

// This file exposes internal seams to the guardduty_test package. Because it
// ends in _test.go it is compiled only under `go test`, so the production build
// never exposes these helpers on the Mock type.

// AddPendingInvitationForTest stages a pending invitation on a detector from the
// given inviter account so a test can exercise the invitee side of
// AcceptInvitation. It wraps the unexported addPendingInvitation.
func (m *Mock) AddPendingInvitationForTest(detectorID, inviter string) (string, error) {
	return m.addPendingInvitation(detectorID, inviter)
}

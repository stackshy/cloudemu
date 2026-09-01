package locks

// lockRequest is the ARM PUT body for a management lock. Only the properties
// block is writable; the id/name/type are server-minted.
type lockRequest struct {
	Properties lockProperties `json:"properties"`
}

// lockProperties carries the writable lock fields. level is CanNotDelete,
// ReadOnly or NotSpecified; notes is free-form. Both round-trip verbatim.
type lockProperties struct {
	Level string `json:"level,omitempty"`
	Notes string `json:"notes,omitempty"`
}

// lockResponse is the ARM representation of a management lock.
type lockResponse struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Properties lockProperties `json:"properties"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []lockResponse `json:"value"`
}

// toResponse projects a stored lock onto its ARM wire representation, minting
// the id from the lock's own scope so it round-trips as addressed.
func toResponse(l storedLock) lockResponse {
	return lockResponse{
		ID:   l.scope + providerSegmentCanonical + l.name,
		Name: l.name,
		Type: armType,
		Properties: lockProperties{
			Level: l.level,
			Notes: l.notes,
		},
	}
}

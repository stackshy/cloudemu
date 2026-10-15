package bedrockagentruntime

// ragSession returns the session to answer in: the caller's, or a new id.
func ragSession(sessionID string) (string, error) {
	if sessionID != "" {
		return sessionID, nil
	}

	return newUUID()
}

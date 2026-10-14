package driver

import "context"

// The interfaces in this package's feature files are optional capabilities of
// a Bedrock Agent runtime provider: the wire handler and the portable wrapper
// discover them by type assertion, so they extend BedrockAgentRuntime without
// widening it.

// Session status values (SessionStatus).
const (
	SessionActive  = "ACTIVE"
	SessionExpired = "EXPIRED"
	SessionEnded   = "ENDED"
)

// SessionConfig describes a session to create.
type SessionConfig struct {
	Metadata         map[string]string
	EncryptionKeyARN string
	Tags             map[string]string
}

// Session is a session of the session-management API (not an InvokeAgent
// conversation): a container for invocations and their steps.
type Session struct {
	ID               string
	ARN              string
	Status           string
	Metadata         map[string]string
	EncryptionKeyARN string
	CreatedAt        string
	LastUpdatedAt    string
}

// Sessions manages session-management sessions.
//
// Identifiers may be session ids (UUIDs) or session ARNs. An ENDED session
// can't be updated.
type Sessions interface {
	CreateSession(ctx context.Context, cfg SessionConfig) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	UpdateSession(ctx context.Context, sessionID string, metadata map[string]string) (*Session, error)
	EndSession(ctx context.Context, sessionID string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, page Page) ([]Session, string, error)
}

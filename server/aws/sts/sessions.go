package sts

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Session is one set of temporary credentials STS minted, retained so the SigV4
// authentication gate can resolve the secret it issued and verify a signature
// made with it (and reject the credential once expired). The session token is
// kept so the gate can bind the request's X-Amz-Security-Token to this session.
type Session struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
	Owner           SessionOwner
}

// SessionOwner is who a session acts as, and whose IAM policies the
// authorization gate evaluates for requests signed with it.
type SessionOwner struct {
	// ARN and UserID are the session's own identity (assumed-role/... or the
	// calling user's), as GetCallerIdentity reports it.
	ARN    string
	UserID string
	// PolicyEntity is the IAM user or role friendly name whose policies govern
	// the session: the assumed role, or the user that called GetSessionToken or
	// GetFederationToken.
	PolicyEntity string
	// Kind is the kind of credential. A role session's role policies are
	// evaluated strictly: a role with no allowing policy (or no such role) is
	// denied, with none of the no-policy bootstrap leniency a long-term user key
	// gets.
	Kind SessionKind
}

// SessionKind is the STS operation a temporary credential came from. It
// decides which STS and IAM operations the credential may call.
type SessionKind int

const (
	// KindNone is not a session: a long-term access key.
	KindNone SessionKind = iota
	// KindRole is an AssumeRole-family session.
	KindRole
	// KindSessionToken is a GetSessionToken session.
	KindSessionToken
	// KindFederation is a GetFederationToken session.
	KindFederation
)

// Forbids reports whether a credential of kind k may never perform action,
// whatever its policies say. From the IAM User Guide, "Compare AWS STS
// credentials":
//
//   - AssumeRole-family credentials cannot call GetFederationToken or
//     GetSessionToken.
//   - GetSessionToken credentials cannot call IAM (cloudemu does not verify
//     MFA, so the MFA exception never applies) and cannot call STS except
//     AssumeRole and GetCallerIdentity.
//   - GetFederationToken credentials cannot call IAM, nor STS except
//     GetCallerIdentity.
//
// sts:TagSession and sts:SetSourceIdentity are permissions of an AssumeRole
// call, not operations, so they follow AssumeRole.
func (k SessionKind) Forbids(action string) bool {
	svc, op, _ := strings.Cut(action, ":")

	isOp := func(names ...string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(op, n) })
	}

	iam, sts := strings.EqualFold(svc, "iam"), strings.EqualFold(svc, "sts")

	switch k {
	case KindRole:
		return sts && isOp(actionGetSessionToken, actionGetFederationToken)
	case KindSessionToken:
		return iam || sts && !isOp(actionAssumeRole, actionGetCallerIdentity, "TagSession", "SetSourceIdentity")
	case KindFederation:
		return iam || sts && !isOp(actionGetCallerIdentity)
	case KindNone:
	}

	return false
}

// SessionStore records the temporary credentials STS issues so their signatures
// can be verified later. It is created only when EnforceAuth is on; with it
// absent STS returns the fixed synthetic credentials it always has (so the
// default, auth-off behavior is byte-for-byte unchanged). Safe for concurrent
// use.
type SessionStore struct {
	mu       sync.RWMutex
	clock    config.Clock
	sessions map[string]Session
}

// NewSessionStore returns an empty store using clock for expiration stamping and
// evaluation. A nil clock falls back to the real clock.
func NewSessionStore(clock config.Clock) *SessionStore {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &SessionStore{clock: clock, sessions: make(map[string]Session)}
}

// Mint generates a unique temporary credential set valid for dur acting as
// owner, records it, and returns it. Each call yields a distinct ASIA access
// key id, a fresh 40-character secret and a long session token, all from
// crypto/rand, so a caller that does not hold the issued secret cannot forge a
// valid signature. It fails closed on a crypto/rand error rather than issuing a
// predictable credential.
func (s *SessionStore) Mint(dur time.Duration, owner SessionOwner) (Session, error) {
	if dur <= 0 {
		dur = sessionDuration
	}

	secret, err := idgen.SecretAccessKey()
	if err != nil {
		return Session{}, err
	}

	token, err := idgen.SessionToken()
	if err != nil {
		return Session{}, err
	}

	sess := Session{
		SecretAccessKey: secret,
		SessionToken:    token,
		Expiration:      s.clock.Now().UTC().Add(dur),
		Owner:           owner,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for range maxKeyIDAttempts {
		id, err := idgen.TempAccessKeyID()
		if err != nil {
			return Session{}, err
		}

		if _, taken := s.sessions[id]; !taken {
			sess.AccessKeyID = id
			s.sessions[id] = sess

			return sess, nil
		}
	}

	return Session{}, errKeyIDExhausted
}

// maxKeyIDAttempts bounds the retries when a freshly drawn access key id is
// already taken. With 80 random bits a single collision is already unlikely.
const maxKeyIDAttempts = 5

// errKeyIDExhausted reports that every attempt drew an id already in use.
var errKeyIDExhausted = errors.New("could not generate a unique temporary access key id")

// Lookup returns the recorded session for id, if any. Expiry is not filtered
// here: the gate compares the returned Expiration against its own clock so it
// can return the expired-token error shape distinctly from an unknown key.
func (s *SessionStore) Lookup(id string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[id]

	return sess, ok
}

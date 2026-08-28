package sts

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
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

// tempKeyRandomLen is the number of random uppercase-alphanumeric characters
// after the ASIA prefix (real STS access key ids are 20 chars: 4 + 16).
const tempKeyRandomLen = 16

// secretLen is the length of a generated temporary secret (real STS secrets are
// 40-character base64-ish strings; any high-entropy value works here).
const secretLen = 40

// sessionTokenRandomLen is the random suffix length of a generated session token.
const sessionTokenRandomLen = 32

// Mint generates a unique temporary credential set valid for dur, records it,
// and returns it. Each call yields a distinct access key id and a fresh
// high-entropy secret, so a caller that does not hold the issued secret cannot
// forge a valid signature.
func (s *SessionStore) Mint(dur time.Duration) Session {
	if dur <= 0 {
		dur = sessionDuration
	}

	sess := Session{
		AccessKeyID:     tempCredentialPrefix + randUpperAlnum(tempKeyRandomLen),
		SecretAccessKey: randUpperAlnum(secretLen),
		SessionToken:    "cloudemu-session-" + randUpperAlnum(sessionTokenRandomLen),
		Expiration:      s.clock.Now().UTC().Add(dur),
	}

	s.mu.Lock()
	s.sessions[sess.AccessKeyID] = sess
	s.mu.Unlock()

	return sess
}

// Lookup returns the recorded session for id, if any. Expiry is not filtered
// here: the gate compares the returned Expiration against its own clock so it
// can return the expired-token error shape distinctly from an unknown key.
func (s *SessionStore) Lookup(id string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[id]

	return sess, ok
}

// tempCredentialPrefix marks STS-issued temporary access key ids (real STS uses
// the same "ASIA" prefix).
const tempCredentialPrefix = "ASIA"

// alnumUpper is the alphabet for generated key ids/secrets (AWS access key ids
// are uppercase alphanumeric).
const alnumUpper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randUpperAlnum returns n cryptographically-random uppercase-alphanumeric
// characters. It draws from crypto/rand; on the practically-impossible read
// error it falls back to the first alphabet character so the result is still
// well-formed (an unverifiable-but-present credential, never a panic).
func randUpperAlnum(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = alnumUpper[0]
		}

		return string(buf)
	}

	for i := range buf {
		buf[i] = alnumUpper[int(buf[i])%len(alnumUpper)]
	}

	return string(buf)
}

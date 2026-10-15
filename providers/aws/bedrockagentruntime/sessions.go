package bedrockagentruntime

import (
	"context"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Session-management constraints from the service model and the user guide's
// quotas.
const (
	uuidPattern        = `[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}`
	sessionARNMarker   = ":session/"
	maxSessionMetadata = 50
	maxSessionTags     = 200
	kmsKeyARNPattern   = `^arn:aws(|-cn|-us-gov):kms:[a-zA-Z0-9-]*:[0-9]{12}:key/[a-zA-Z0-9-]{36}$`
	sessionIdleTimeout = time.Hour
	defaultListPage    = 1000
)

var (
	uuidRE              = regexp.MustCompile(`^` + uuidPattern + `$`)
	kmsKeyARNRE         = regexp.MustCompile(kmsKeyARNPattern)
	sessionIdentifierRE = regexp.MustCompile(`^(arn:aws(-[^:]+)?:bedrock:[a-z0-9-]+:[0-9]{12}:session/` + uuidPattern +
		`)$|^` + uuidPattern + `$`)
)

// CreateSession starts an ACTIVE session.
func (m *Mock) CreateSession(_ context.Context, cfg driver.SessionConfig) (*driver.Session, error) {
	var v violations

	if len(cfg.Metadata) > maxSessionMetadata {
		v.addf("Value at 'sessionMetadata' failed to satisfy constraint: Member must have length less than or equal to %d",
			maxSessionMetadata)
	}

	v.pattern("encryptionKeyArn", cfg.EncryptionKeyARN, kmsKeyARNRE, kmsKeyARNPattern)

	if err := v.err(); err != nil {
		return nil, err
	}

	if err := validateTagValues(cfg.Tags); err != nil {
		return nil, err
	}

	id, err := newUUID()
	if err != nil {
		return nil, err
	}

	now := m.nowString()
	s := &driver.Session{
		ID:               id,
		ARN:              idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "session/"+id),
		Status:           driver.SessionActive,
		Metadata:         maps.Clone(cfg.Metadata),
		EncryptionKeyARN: cfg.EncryptionKeyARN,
		CreatedAt:        now,
		LastUpdatedAt:    now,
	}
	m.sessions.Set(id, s)

	if len(cfg.Tags) > 0 {
		m.sessionTags.Set(s.ARN, maps.Clone(cfg.Tags))
	}

	result := m.observeSession(s)

	return &result, nil
}

// GetSession returns a session.
func (m *Mock) GetSession(_ context.Context, sessionID string) (*driver.Session, error) {
	s, err := m.findSession(sessionID)
	if err != nil {
		return nil, err
	}

	result := m.observeSession(s)

	return &result, nil
}

// UpdateSession replaces the session's metadata. An ended or expired session
// can't be updated (ConflictException).
func (m *Mock) UpdateSession(_ context.Context, sessionID string, metadata map[string]string) (*driver.Session, error) {
	if len(metadata) > maxSessionMetadata {
		var v violations

		v.addf("Value at 'sessionMetadata' failed to satisfy constraint: Member must have length less than or equal to %d",
			maxSessionMetadata)

		return nil, v.err()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.activeSession(sessionID)
	if err != nil {
		return nil, err
	}

	updated := cloneSession(s)
	updated.Metadata = maps.Clone(metadata)
	updated.LastUpdatedAt = m.nowString()
	m.sessions.Set(updated.ID, &updated)

	result := m.observeSession(&updated)

	return &result, nil
}

// EndSession ends a session. Its content stays readable but nothing can be
// added to it. Ending an ended session is a ConflictException.
func (m *Mock) EndSession(_ context.Context, sessionID string) (*driver.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.activeSession(sessionID)
	if err != nil {
		return nil, err
	}

	ended := cloneSession(s)
	ended.Status = driver.SessionEnded
	ended.LastUpdatedAt = m.nowString()
	m.sessions.Set(ended.ID, &ended)

	result := m.observeSession(&ended)

	return &result, nil
}

// DeleteSession deletes an ended (or expired) session with its invocations,
// steps and tags. An ACTIVE session must be ended first (ConflictException).
func (m *Mock) DeleteSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.findSession(sessionID)
	if err != nil {
		return err
	}

	if m.observeSession(s).Status == driver.SessionActive {
		return errors.Newf(errors.FailedPrecondition,
			"Session %s is ACTIVE. End it with EndSession before you delete it.", s.ID)
	}

	m.sessions.Delete(s.ID)
	m.sessionTags.Delete(s.ARN)
	deleteByPrefix(m.invocations, s.ID+"/")
	deleteByPrefix(m.steps, s.ID+"/")

	return nil
}

// ListSessions lists one page of sessions, newest first.
func (m *Mock) ListSessions(_ context.Context, page driver.Page) ([]driver.Session, string, error) {
	all := m.sessions.SortedValues()
	out := make([]driver.Session, 0, len(all))

	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, m.observeSession(all[i]))
	}

	sortByTimeDesc(out, func(s driver.Session) string { return s.CreatedAt })

	return paginate(out, page, defaultListPage)
}

// findSession resolves a session id or ARN.
func (m *Mock) findSession(identifier string) (*driver.Session, error) {
	var v violations

	v.required("sessionIdentifier", identifier == "")
	v.pattern("sessionIdentifier", identifier, sessionIdentifierRE, "session id or ARN")

	if err := v.err(); err != nil {
		return nil, err
	}

	id := identifier
	if _, after, ok := strings.Cut(identifier, sessionARNMarker); ok {
		id = after
	}

	s, ok := m.sessions.Get(id)
	if !ok || (strings.HasPrefix(identifier, "arn:") && s.ARN != identifier) {
		return nil, errors.Newf(errors.NotFound, "session %q not found", identifier)
	}

	return s, nil
}

// activeSession resolves a session that can still change. Callers hold m.mu.
func (m *Mock) activeSession(identifier string) (*driver.Session, error) {
	s, err := m.findSession(identifier)
	if err != nil {
		return nil, err
	}

	if status := m.observeSession(s).Status; status != driver.SessionActive {
		return nil, errors.Newf(errors.FailedPrecondition, "Session %s is %s and can't be changed.", s.ID, status)
	}

	return s, nil
}

// observeSession returns a copy with the status a reader sees now: an ACTIVE
// session idle for over an hour is EXPIRED.
func (m *Mock) observeSession(s *driver.Session) driver.Session {
	out := cloneSession(s)

	if out.Status == driver.SessionActive {
		if last, err := time.Parse(time.RFC3339Nano, out.LastUpdatedAt); err == nil && m.now().After(last.Add(sessionIdleTimeout)) {
			out.Status = driver.SessionExpired
		}
	}

	return out
}

func cloneSession(s *driver.Session) driver.Session {
	out := *s
	out.Metadata = maps.Clone(s.Metadata)

	return out
}

// touchSession records activity on a session. Callers hold m.mu.
func (m *Mock) touchSession(s *driver.Session) {
	updated := cloneSession(s)
	updated.LastUpdatedAt = m.nowString()
	m.sessions.Set(updated.ID, &updated)
}

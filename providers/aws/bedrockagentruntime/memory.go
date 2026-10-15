package bedrockagentruntime

import (
	"context"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// memoryIDPattern is the MemoryId pattern; memory ids are 2..100 characters.
const memoryIDPattern = `^[0-9a-zA-Z._:-]+$`

var memoryIDRE = regexp.MustCompile(memoryIDPattern)

// GetAgentMemory lists the session summaries kept under a memory id, newest
// session first. Summaries are written when a conversation with that memory
// id ends on an agent whose memory is enabled.
func (m *Mock) GetAgentMemory(ctx context.Context, agentID, aliasID, memoryID, memoryType string, page driver.Page) (
	[]driver.MemorySummary, string, error,
) {
	var v violations

	v.agentID("agentId", agentID)
	v.agentID("agentAliasId", aliasID)
	v.memoryID(memoryID, true)
	v.required("memoryType", memoryType == "")
	v.enum("memoryType", memoryType, driver.MemoryTypeSessionSummary)

	if err := v.err(); err != nil {
		return nil, "", err
	}

	if err := m.checkAlias(ctx, agentID, aliasID); err != nil {
		return nil, "", err
	}

	m.endIdleConversations(ctx, agentID, aliasID)

	prefix := agentID + "/" + aliasID + "/" + memoryID + "/"

	var out []driver.MemorySummary

	for key, s := range m.memories.All() {
		if strings.HasPrefix(key, prefix) {
			out = append(out, *s)
		}
	}

	sortByTimeDesc(out, func(s driver.MemorySummary) string { return s.SessionStartTime + s.SessionID })

	return paginate(out, page, defaultListPage)
}

// DeleteAgentMemory deletes the summaries of a memory id (or of every memory
// id), optionally of one session only.
func (m *Mock) DeleteAgentMemory(ctx context.Context, agentID, aliasID, memoryID, sessionID string) error {
	var v violations

	v.agentID("agentId", agentID)
	v.agentID("agentAliasId", aliasID)
	v.memoryID(memoryID, false)

	if sessionID != "" {
		v.sessionID(sessionID)
	}

	if err := v.err(); err != nil {
		return err
	}

	if err := m.checkAlias(ctx, agentID, aliasID); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := agentID + "/" + aliasID + "/"

	for key, s := range m.memories.All() {
		if strings.HasPrefix(key, prefix) && matchesMemory(s, memoryID, sessionID) {
			m.memories.Delete(key)
		}
	}

	return nil
}

// matchesMemory reports whether a summary is of memoryID and sessionID (an
// empty filter matches every value).
func matchesMemory(s *driver.MemorySummary, memoryID, sessionID string) bool {
	return (memoryID == "" || s.MemoryID == memoryID) && (sessionID == "" || s.SessionID == sessionID)
}

// checkAlias resolves the agent and alias when the control plane is linked.
func (m *Mock) checkAlias(ctx context.Context, agentID, aliasID string) error {
	if m.cp == nil {
		return nil
	}

	if _, err := m.cp.GetAgent(ctx, agentID); err != nil {
		return err
	}

	if aliasID == "TSTALIASID" {
		return nil
	}

	_, err := m.cp.GetAgentAlias(ctx, agentID, aliasID)

	return err
}

func (v *violations) memoryID(value string, required bool) {
	if required {
		v.required("memoryId", value == "")
	}

	if value != "" && len(value) < minSessionIDLen {
		v.addf("Value '%s' at 'memoryId' failed to satisfy constraint: Member must have length greater than or equal to %d",
			value, minSessionIDLen)
	}

	v.maxLen("memoryId", value, maxSessionIDLen)
	v.pattern("memoryId", value, memoryIDRE, memoryIDPattern)
}

// endIdleConversations ends the alias's conversations that passed the agent's
// idle TTL, writing their session summaries, as AWS does when a session times
// out; otherwise a timed-out session would show in memory only once its
// sessionId was invoked again. An agent the runtime can't resolve (for example
// an unprepared draft) is left alone.
func (m *Mock) endIdleConversations(ctx context.Context, agentID, aliasID string) {
	if m.cp == nil {
		return
	}

	cfg, err := m.resolveAgent(ctx, agentID, aliasID)
	if err != nil {
		return
	}

	prefix := agentID + "/" + aliasID + "/"

	for key := range m.conversations.All() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		m.endIfIdle(cfg, key)
	}
}

// endIfIdle ends one conversation when it is idle. A conversation with a turn
// in progress is not idle, so it is skipped rather than waited for: waiting
// would block behind a slow turn, and deadlock when that turn's action-group
// Lambda is the one reading agent memory.
func (m *Mock) endIfIdle(cfg *agentConfig, key string) {
	conv, ok := m.conversations.Get(key)
	if !ok {
		return
	}

	if c := cloneConversation(conv); !m.idledOut(cfg, &c) {
		return
	}

	unlock, locked := m.tryLockConversation(key)
	if !locked {
		return
	}
	defer unlock()

	// Read again under the lock: a turn may have run since.
	if conv, ok = m.conversations.Get(key); ok {
		c := cloneConversation(conv)
		if m.idledOut(cfg, &c) {
			m.endConversation(cfg, &c)
			m.conversations.Delete(key)
		}
	}
}

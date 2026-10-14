package driver

import "context"

// MemoryTypeSessionSummary is the only memory type Bedrock agents keep.
const MemoryTypeSessionSummary = "SESSION_SUMMARY"

// MemorySummary is the summary of one ended agent session.
type MemorySummary struct {
	MemoryID         string
	SessionID        string
	SummaryText      string
	SessionStartTime string
	SessionExpiry    string
}

// AgentMemory reads and deletes an agent's session-summary memory.
//
// An agent with memory enabled keeps one summary per ended session, per
// memoryId.
type AgentMemory interface {
	GetAgentMemory(ctx context.Context, agentID, aliasID, memoryID, memoryType string, page Page) (
		[]MemorySummary, string, error)
	DeleteAgentMemory(ctx context.Context, agentID, aliasID, memoryID, sessionID string) error
}

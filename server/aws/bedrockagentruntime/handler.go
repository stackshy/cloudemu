// Package bedrockagentruntime implements the AWS Bedrock Agent runtime
// (bedrock-agent-runtime) restJson1 data-plane API as a server.Handler. Point
// the real aws-sdk-go-v2/service/bedrockagentruntime client at a Server
// registered with this handler and InvokeAgent (an eventstream response),
// Retrieve, and RetrieveAndGenerate all work end-to-end against an in-memory
// driver.
//
// URL shapes follow what the SDK emits:
//
//	POST /agents/{agentId}/agentAliases/{agentAliasId}/sessions/{sessionId}/text — InvokeAgent
//	POST /knowledgebases/{knowledgeBaseId}/retrieve                              — Retrieve
//	POST /retrieveAndGenerate                                                    — RetrieveAndGenerate
//
// The Matches predicate is intentionally SPECIFIC to these suffixes (.../text,
// .../retrieve) and the exact /retrieveAndGenerate path so it does not collide
// with a bedrock-agent CONTROL-PLANE handler that also owns /agents/ and
// /knowledgebases. The orchestrator registers this runtime handler before the
// control-plane handler so the specific matches win.
package bedrockagentruntime

import (
	"net/http"
	"strings"

	bedrockagentruntimedriver "github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

const (
	contentTypeJSON        = "application/json"
	contentTypeEventStream = "application/vnd.amazon.eventstream"
	maxBodyBytes           = 5 << 20

	prefixAgents         = "/agents/"
	prefixKnowledgeBases = "/knowledgebases/"
	suffixText           = "/text"
	suffixRetrieve       = "/retrieve"
	pathRetrieveAndGen   = "/retrieveAndGenerate"
)

// Handler serves AWS Bedrock Agent runtime restJson1 requests against a driver.
type Handler struct {
	runtime bedrockagentruntimedriver.BedrockAgentRuntime
}

// New returns a Bedrock Agent runtime handler backed by drv.
func New(drv bedrockagentruntimedriver.BedrockAgentRuntime) *Handler {
	return &Handler{runtime: drv}
}

// isInvokeAgentPath reports whether p is an InvokeAgent path.
func isInvokeAgentPath(p string) bool {
	return strings.HasPrefix(p, prefixAgents) && strings.HasSuffix(p, suffixText)
}

// isRetrievePath reports whether p is a Retrieve path.
func isRetrievePath(p string) bool {
	return strings.HasPrefix(p, prefixKnowledgeBases) && strings.HasSuffix(p, suffixRetrieve)
}

// claims reports whether path p belongs to this handler.
func claims(p string) bool {
	return isInvokeAgentPath(p) || isRetrievePath(p) || p == pathRetrieveAndGen
}

// Matches claims only the Bedrock Agent runtime data-plane paths. Every runtime
// operation is POST, so requiring POST here prevents shadowing a control-plane
// GET/DELETE whose resource id happens to end in "text" or "retrieve".
func (*Handler) Matches(r *http.Request) bool {
	return r.Method == http.MethodPost && claims(r.URL.Path)
}

// ServeHTTP routes by URL shape. Every runtime path is POST-only.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch {
	case isInvokeAgentPath(p):
		h.serveInvokeAgent(w, r, p)
	case isRetrievePath(p):
		h.serveRetrieve(w, r, p)
	case p == pathRetrieveAndGen:
		h.retrieveAndGenerate(w, r)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+p)
	}
}

// serveInvokeAgent parses the path
// /agents/{agentId}/agentAliases/{agentAliasId}/sessions/{sessionId}/text and
// dispatches to the InvokeAgent operation.
func (h *Handler) serveInvokeAgent(w http.ResponseWriter, r *http.Request, p string) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 7 || parts[0] != "agents" || parts[2] != "agentAliases" ||
		parts[4] != "sessions" || parts[6] != "text" {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported InvokeAgent path")

		return
	}

	h.invokeAgent(w, r, parts[1], parts[3], parts[5])
}

// serveRetrieve parses the path /knowledgebases/{knowledgeBaseId}/retrieve and
// dispatches to the Retrieve operation.
func (h *Handler) serveRetrieve(w http.ResponseWriter, r *http.Request, p string) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 3 || parts[0] != "knowledgebases" || parts[2] != "retrieve" {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Retrieve path")

		return
	}

	h.retrieve(w, r, parts[1])
}

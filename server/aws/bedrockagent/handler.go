// Package bedrockagent implements the AWS Bedrock Agent authoring restJson1
// control-plane API as a server.Handler. Point the real
// aws-sdk-go-v2/service/bedrockagent client at a Server registered with this
// handler and the agent, knowledge-base, data-source, flow, and prompt
// authoring lifecycles work end-to-end against an in-memory driver.
//
// Routing is by (HTTP method, path-template); method disambiguates same-path
// operations. URL shapes follow what the SDK emits:
//
//	PUT    /agents/                                   — CreateAgent
//	POST   /agents/                                   — ListAgents
//	GET    /agents/{agentId}/                         — GetAgent
//	PUT    /agents/{agentId}/                         — UpdateAgent
//	DELETE /agents/{agentId}/                         — DeleteAgent
//	POST   /agents/{agentId}/                         — PrepareAgent
//	PUT    /agents/{agentId}/agentaliases/            — CreateAgentAlias
//	PUT    /knowledgebases/                           — CreateKnowledgeBase
//	POST   /knowledgebases/                           — ListKnowledgeBases
//	GET    /knowledgebases/{id}                       — GetKnowledgeBase
//	PUT    /knowledgebases/{id}                       — UpdateKnowledgeBase
//	DELETE /knowledgebases/{id}                       — DeleteKnowledgeBase
//	PUT    /knowledgebases/{kb}/datasources/          — CreateDataSource
//	POST   /knowledgebases/{kb}/datasources/          — ListDataSources
//	GET    /knowledgebases/{kb}/datasources/{ds}      — GetDataSource
//	PUT    /knowledgebases/{kb}/datasources/{ds}      — UpdateDataSource
//	DELETE /knowledgebases/{kb}/datasources/{ds}      — DeleteDataSource
//	PUT    /knowledgebases/{kb}/datasources/{ds}/ingestionjobs/ — StartIngestionJob
//	POST   /flows/                                    — CreateFlow
//	GET    /flows/                                    — ListFlows
//	GET    /flows/{id}/                               — GetFlow
//	PUT    /flows/{id}/                               — UpdateFlow
//	DELETE /flows/{id}/                               — DeleteFlow
//	POST   /flows/{id}/                               — PrepareFlow
//	POST   /prompts/                                  — CreatePrompt
//	GET    /prompts/                                  — ListPrompts
//	GET    /prompts/{id}/                             — GetPrompt
//	PUT    /prompts/{id}/                             — UpdatePrompt
//	DELETE /prompts/{id}/                             — DeletePrompt
package bedrockagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	prefixAgents  = "/agents"
	prefixKB      = "/knowledgebases"
	prefixFlows   = "/flows"
	prefixPrompts = "/prompts"

	segAgentAliases  = "agentaliases"
	segDataSources   = "datasources"
	segIngestionJobs = "ingestionjobs"

	// segment counts identifying the nested data-source path shapes.
	dsCollectionSegments = 2 // {kb}/datasources
	dsItemSegments       = 3 // {kb}/datasources/{ds}
	ingestionSegments    = 4 // {kb}/datasources/{ds}/ingestionjobs
)

// Handler serves AWS Bedrock Agent restJson1 requests against a driver.
type Handler struct {
	agent badriver.BedrockAgent
}

// New returns a Bedrock Agent handler backed by drv.
func New(drv badriver.BedrockAgent) *Handler {
	return &Handler{agent: drv}
}

// Matches claims the Bedrock Agent authoring URL prefixes.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return underPrefix(p, prefixAgents) ||
		underPrefix(p, prefixKB) ||
		underPrefix(p, prefixFlows) ||
		underPrefix(p, prefixPrompts)
}

// underPrefix reports whether p equals pre or is a child path of pre. It
// anchors bare prefixes so bucket-style paths (e.g. "/flows-prod") fall
// through to later handlers instead of being swallowed here.
func underPrefix(p, pre string) bool {
	return p == pre || strings.HasPrefix(p, pre+"/")
}

// ServeHTTP routes by URL prefix.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	switch {
	case underPrefix(p, prefixAgents):
		h.serveAgents(w, r, segments(p, prefixAgents))
	case underPrefix(p, prefixKB):
		h.serveKnowledgeBases(w, r, segments(p, prefixKB))
	case underPrefix(p, prefixFlows):
		h.serveFlows(w, r, segments(p, prefixFlows))
	case underPrefix(p, prefixPrompts):
		h.servePrompts(w, r, segments(p, prefixPrompts))
	default:
		notFound(w, p)
	}
}

// segments trims prefix and surrounding slashes from path and splits the
// remainder into path segments. An empty remainder yields a nil slice.
func segments(path, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return nil
	}

	return strings.Split(rest, "/")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// decodeBody decodes the request body but tolerates an empty body (some ops
// such as PrepareAgent send no payload).
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return true
	}

	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

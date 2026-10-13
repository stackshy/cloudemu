// Package bedrockagent implements the AWS Bedrock Agent authoring restJson1
// control-plane API as a server.Handler. Point the real
// aws-sdk-go-v2/service/bedrockagent client at a Server registered with this
// handler and the agent, knowledge-base, data-source, flow, and prompt
// authoring lifecycles work end-to-end against an in-memory driver.
//
// Routing is by (HTTP method, path-template); method disambiguates same-path
// operations. URL shapes follow what the SDK emits:
//
//	PUT    /agents/                                     CreateAgent
//	POST   /agents/                                     ListAgents
//	GET    /agents/{agentId}/                           GetAgent
//	PUT    /agents/{agentId}/                           UpdateAgent
//	DELETE /agents/{agentId}/                           DeleteAgent
//	POST   /agents/{agentId}/                           PrepareAgent
//	PUT    /agents/{agentId}/agentaliases/              CreateAgentAlias
//	PUT    /knowledgebases/                             CreateKnowledgeBase
//	POST   /knowledgebases/                             ListKnowledgeBases
//	GET    /knowledgebases/{id}                         GetKnowledgeBase
//	PUT    /knowledgebases/{id}                         UpdateKnowledgeBase
//	DELETE /knowledgebases/{id}                         DeleteKnowledgeBase
//	PUT    /knowledgebases/{kb}/datasources/            CreateDataSource
//	POST   /knowledgebases/{kb}/datasources/            ListDataSources
//	GET    /knowledgebases/{kb}/datasources/{ds}        GetDataSource
//	PUT    /knowledgebases/{kb}/datasources/{ds}        UpdateDataSource
//	DELETE /knowledgebases/{kb}/datasources/{ds}        DeleteDataSource
//	PUT    /knowledgebases/{kb}/datasources/{ds}/ingestionjobs/   StartIngestionJob
//	POST   /flows/                                      CreateFlow
//	GET    /flows/                                      ListFlows
//	GET    /flows/{id}/                                 GetFlow
//	PUT    /flows/{id}/                                 UpdateFlow
//	DELETE /flows/{id}/                                 DeleteFlow
//	POST   /flows/{id}/                                 PrepareFlow
//	POST   /prompts/                                    CreatePrompt
//	GET    /prompts/                                    ListPrompts
//	GET    /prompts/{id}/                               GetPrompt
//	PUT    /prompts/{id}/                               UpdatePrompt
//	DELETE /prompts/{id}/                               DeletePrompt
//	POST   /tags/{resourceArn}                          TagResource
//	DELETE /tags/{resourceArn}?tagKeys=                 UntagResource
//	GET    /tags/{resourceArn}                          ListTagsForResource
package bedrockagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

	segAgentAliases       = "agentaliases"
	segAgentVersions      = "agentversions"
	segActionGroups       = "actiongroups"
	segAgentKBs           = "knowledgebases"
	segAgentCollaborators = "agentcollaborators"
	segDataSources        = "datasources"
	segIngestionJobs      = "ingestionjobs"
	segDocuments          = "documents"
	segStop               = "stop"
	segVersions           = "versions"
	segAliases            = "aliases"

	// segment counts identifying the nested data-source path shapes.
	dsCollectionSegments = 2 // {kb}/datasources
	dsItemSegments       = 3 // {kb}/datasources/{ds}
	ingestionSegments    = 4 // {kb}/datasources/{ds}/ingestionjobs
	ingestionJobSegments = 5 // {kb}/datasources/{ds}/ingestionjobs/{job}
	ingestionStopSegs    = 6 // {kb}/datasources/{ds}/ingestionjobs/{job}/stop
	documentsSegments    = 4 // {kb}/datasources/{ds}/documents
	documentsOpSegments  = 5 // {kb}/datasources/{ds}/documents/{getDocuments|deleteDocuments}
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
//
// The claimed REST roots /agents, /knowledgebases, /flows, and /prompts are
// anchored via underPrefix. As with the sibling /custom-models and EKS
// /clusters handlers, this means an S3 bucket named EXACTLY agents,
// knowledgebases, flows, or prompts under path-style addressing is claimed
// here before reaching the S3 catch-all. This is the accepted REST-vs-catch-all
// tradeoff; callers needing those exact bucket names should use
// virtual-host-style addressing.
//
// The shared /tags/{resourceArn} path is claimed only for bedrock-agent ARNs
// (see ownsTagsPath), so other services' tag requests fall through.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return ownsTagsPath(p) || ownsPolicyPath(p) ||
		underPrefix(p, prefixAgents) ||
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
	case ownsTagsPath(p):
		h.serveTags(w, r)
	case ownsPolicyPath(p):
		h.serveResourcePolicy(w, r)
	case underPrefix(p, prefixAgents):
		h.serveAgents(w, r, segments(r, prefixAgents))
	case underPrefix(p, prefixKB):
		h.serveKnowledgeBases(w, r, segments(r, prefixKB))
	case underPrefix(p, prefixFlows):
		h.serveFlows(w, r, segments(r, prefixFlows))
	case underPrefix(p, prefixPrompts):
		h.servePrompts(w, r, segments(r, prefixPrompts))
	default:
		notFound(w, p)
	}
}

// segments trims prefix and surrounding slashes from the request path and
// splits the remainder into unescaped path segments. It splits the escaped
// path, so an identifier sent as an ARN (its "/" escaped as %2F by the SDK)
// stays one segment. An empty remainder yields a nil slice.
func segments(r *http.Request, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), prefix), "/")
	if rest == "" {
		return nil
	}

	parts := strings.Split(rest, "/")
	for i, part := range parts {
		if u, err := url.PathUnescape(part); err == nil {
			parts[i] = u
		}
	}

	return parts
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
	writeJSONStatus(w, http.StatusOK, v)
}

// writeJSONStatus writes v with the success status the service model gives
// the operation (200, 201 or 202).
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeNoContent answers an operation whose model response is 204 with no
// body.
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// capabilityOf returns the optional capability T of the backing driver, or
// answers the call with a ValidationException when it has none.
func capabilityOf[T any](d badriver.BedrockAgent, w http.ResponseWriter, op string) (T, bool) {
	c, ok := d.(T)
	if !ok {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"The operation "+op+" is not supported by this Bedrock Agent backend.")
	}

	return c, ok
}

// queryBool reads a boolean query parameter such as skipResourceInUseCheck.
func queryBool(r *http.Request, name string) bool {
	v, err := strconv.ParseBool(r.URL.Query().Get(name))

	return err == nil && v
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return iamServicePrefix }

// iamServicePrefix is the IAM namespace of the Agents for Bedrock actions.
const iamServicePrefix = "bedrock"

// Package bedrockagentruntime implements the AWS Bedrock Agent runtime
// (bedrock-agent-runtime) restJson1 data-plane API as a server.Handler. Point
// the real aws-sdk-go-v2/service/bedrockagentruntime client at a Server
// registered with this handler and every runtime operation works end-to-end
// against an in-memory driver; the eventstream operations (InvokeAgent,
// InvokeInlineAgent, InvokeFlow, RetrieveAndGenerateStream, OptimizePrompt,
// AgenticRetrieveStream) stream their real event types.
//
// Routes (method + path shape, as the SDK emits them):
//
//	POST   /agents/{agentId}/agentAliases/{aliasId}/sessions/{sessionId}/text   InvokeAgent
//	POST   /agents/{sessionId}                       (no trailing slash)        InvokeInlineAgent
//	GET    /agents/{agentId}/agentAliases/{aliasId}/memories                    GetAgentMemory
//	DELETE /agents/{agentId}/agentAliases/{aliasId}/memories                    DeleteAgentMemory
//	POST   /knowledgebases/{kbId}/retrieve                                      Retrieve
//	POST   /knowledgebases/{kbId}/datasources/{dsId}/documents/{docId}/content  GetDocumentContent
//	GET    (same path)?X-Cloudemu-Content=                                      the presigned content URL
//	POST   /retrieveAndGenerate | /retrieveAndGenerateStream                    RetrieveAndGenerate[Stream]
//	POST   /rerank | /generateQuery | /optimize-prompt | /agenticRetrieveStream
//	PUT|POST /sessions/                                                         CreateSession | ListSessions
//	GET|PUT|DELETE /sessions/{id}/ , PATCH /sessions/{id}                       Get/Update/Delete/EndSession
//	PUT|POST /sessions/{id}/invocations/                                        CreateInvocation | ListInvocations
//	PUT|POST /sessions/{id}/invocationSteps/ , POST .../{stepId}                PutInvocationStep | List | Get
//	POST   /flows/{flowId}/aliases/{aliasId}                                    InvokeFlow
//	POST   /flows/{flowId}/aliases/{aliasId}/executions                         StartFlowExecution
//	GET    /flows/{flowId}/aliases/{aliasId}/executions/{id}[/events|/flowsnapshot]
//	POST   /flows/{flowId}/aliases/{aliasId}/executions/{id}/stop               StopFlowExecution
//	GET    /flows/{flowId}/executions                                           ListFlowExecutions
//	GET|POST|DELETE /tags/{sessionArn}                                          tagging
//
// Matches claims only these shapes and methods, so it stays disjoint from the
// bedrock-agent control plane on the shared /agents, /knowledgebases and
// /flows roots (whose paths end in lower-case segments or trailing slashes),
// from EKS and bedrock-agent on /tags, and from S3: the roots that are also
// valid bucket names (sessions, rerank, optimize-prompt) are claimed only for
// requests signed for bedrock, or unsigned JSON requests.
package bedrockagentruntime

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	bedrockagentruntimedriver "github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

const (
	contentTypeJSON        = "application/json"
	contentTypeEventStream = "application/vnd.amazon.eventstream"
	maxBodyBytes           = 5 << 20
	signingService         = "bedrock"
	prefixTags             = "/tags/"
)

// Handler serves AWS Bedrock Agent runtime restJson1 requests against a driver.
type Handler struct {
	runtime bedrockagentruntimedriver.BedrockAgentRuntime
}

// New returns a Bedrock Agent runtime handler backed by drv.
func New(drv bedrockagentruntimedriver.BedrockAgentRuntime) *Handler {
	return &Handler{runtime: drv}
}

// Matches claims only the runtime's own path shapes and methods.
func (*Handler) Matches(r *http.Request) bool {
	if ownsTagsPath(r.URL.Path) {
		return true
	}

	rt := findRoute(r)
	if rt == nil {
		return false
	}

	return !rt.bucketLike || claimsBucketLike(r)
}

// ServeHTTP dispatches to the matched route.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ownsTagsPath(r.URL.Path) {
		h.serveTags(w, r)

		return
	}

	rt := findRoute(r)
	if rt == nil {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+r.URL.Path)

		return
	}

	rt.serve(h, w, r, segments(r))
}

// claimsBucketLike reports whether a request to a root that is also a valid S3
// bucket name is for the runtime: signed for bedrock, or unsigned JSON.
func claimsBucketLike(r *http.Request) bool {
	switch sigv4.Service(r) {
	case signingService:
		return true
	case "":
		return strings.HasPrefix(r.Header.Get("Content-Type"), contentTypeJSON)
	default:
		return false
	}
}

// segments splits the escaped request path, so an identifier sent as an ARN
// (its "/" escaped as %2F by the SDK) stays one segment.
func segments(r *http.Request) []string {
	rest := strings.Trim(r.URL.EscapedPath(), "/")
	if rest == "" {
		return nil
	}

	parts := strings.Split(rest, "/")
	for i, p := range parts {
		if u, err := url.PathUnescape(p); err == nil {
			parts[i] = u
		}
	}

	return parts
}

// capabilityOf returns the optional capability T of the driver, or answers
// that this backend does not support op.
func capabilityOf[T any](d bedrockagentruntimedriver.BedrockAgentRuntime, w http.ResponseWriter, op string) (T, bool) {
	c, ok := d.(T)
	if !ok {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"The operation "+op+" is not supported by this Bedrock Agent runtime backend.")
	}

	return c, ok
}

// withCapability runs fn with the driver's capability T.
func withCapability[T any](h *Handler, w http.ResponseWriter, op string, fn func(T)) {
	if c, ok := capabilityOf[T](h.runtime, w, op); ok {
		fn(c)
	}
}

// queryPage reads maxResults/nextToken (or maxItems) query parameters.
func queryPage(w http.ResponseWriter, r *http.Request, sizeParam string) (bedrockagentruntimedriver.Page, bool) {
	q := r.URL.Query()
	page := bedrockagentruntimedriver.Page{NextToken: q.Get("nextToken")}

	if raw := q.Get(sizeParam); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"1 validation error detected: Value '"+raw+"' at '"+sizeParam+"' failed to satisfy constraint: Member must be an integer")

			return page, false
		}

		size := int32(n)
		page.MaxResults = &size
	}

	return page, true
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return signingService }

package bedrockagentruntime

import (
	"net/http"
	"strings"
)

// route is one runtime operation's method and path shape. A "*" segment
// matches any value.
type route struct {
	method string
	shape  []string
	// noTrailingSlash keeps InvokeInlineAgent (/agents/{sessionId}) apart from
	// the control plane's PrepareAgent (/agents/{agentId}/).
	noTrailingSlash bool
	// query, when set, must be present (the presigned document-content URL).
	query string
	// bucketLike marks a root that is also a valid S3 bucket name.
	bucketLike bool
	serve      func(h *Handler, w http.ResponseWriter, r *http.Request, segs []string)
}

// routes lists every runtime route; findRoute returns the first that fits.
//
//nolint:gochecknoglobals // fixed routing table.
var routes = []route{
	{method: http.MethodPost, shape: []string{"agents", "*", "agentAliases", "*", "sessions", "*", "text"}, serve: serveInvokeAgent},
	{method: http.MethodPost, shape: []string{"agents", "*"}, noTrailingSlash: true, serve: serveInlineAgent},
	{method: http.MethodGet, shape: []string{"agents", "*", "agentAliases", "*", "memories"}, serve: serveGetMemory},
	{method: http.MethodDelete, shape: []string{"agents", "*", "agentAliases", "*", "memories"}, serve: serveDeleteMemory},
	{method: http.MethodPost, shape: []string{"knowledgebases", "*", "retrieve"}, serve: serveRetrieve},
	{method: http.MethodPost, shape: docContentShape, serve: serveDocumentContent},
	{method: http.MethodGet, shape: docContentShape, query: tokenParam, serve: serveDocumentDownload},
	{method: http.MethodPost, shape: []string{"retrieveAndGenerate"}, serve: serveRAG},
	{method: http.MethodPost, shape: []string{"retrieveAndGenerateStream"}, serve: serveRAGStream},
	{method: http.MethodPost, shape: []string{"rerank"}, bucketLike: true, serve: serveRerank},
	{method: http.MethodPost, shape: []string{"generateQuery"}, serve: serveGenerateQuery},
	{method: http.MethodPost, shape: []string{"optimize-prompt"}, bucketLike: true, serve: serveOptimizePrompt},
	{method: http.MethodPost, shape: []string{"agenticRetrieveStream"}, serve: serveAgenticRetrieve},
	{method: http.MethodPut, shape: []string{"sessions"}, bucketLike: true, serve: serveCreateSession},
	{method: http.MethodPost, shape: []string{"sessions"}, bucketLike: true, serve: serveListSessions},
	{method: http.MethodGet, shape: []string{"sessions", "*"}, bucketLike: true, serve: serveGetSession},
	{method: http.MethodPut, shape: []string{"sessions", "*"}, bucketLike: true, serve: serveUpdateSession},
	{method: http.MethodDelete, shape: []string{"sessions", "*"}, bucketLike: true, serve: serveDeleteSession},
	{method: http.MethodPatch, shape: []string{"sessions", "*"}, bucketLike: true, serve: serveEndSession},
	{method: http.MethodPut, shape: []string{"sessions", "*", "invocations"}, bucketLike: true, serve: serveCreateInvocation},
	{method: http.MethodPost, shape: []string{"sessions", "*", "invocations"}, bucketLike: true, serve: serveListInvocations},
	{method: http.MethodPut, shape: []string{"sessions", "*", "invocationSteps"}, bucketLike: true, serve: servePutStep},
	{method: http.MethodPost, shape: []string{"sessions", "*", "invocationSteps"}, bucketLike: true, serve: serveListSteps},
	{method: http.MethodPost, shape: []string{"sessions", "*", "invocationSteps", "*"}, bucketLike: true, serve: serveGetStep},
	{method: http.MethodPost, shape: []string{"flows", "*", "aliases", "*"}, serve: serveInvokeFlow},
	{method: http.MethodPost, shape: []string{"flows", "*", "aliases", "*", "executions"}, serve: serveStartExecution},
	{method: http.MethodGet, shape: []string{"flows", "*", "aliases", "*", "executions", "*"}, serve: serveGetExecution},
	{method: http.MethodGet, shape: []string{"flows", "*", "aliases", "*", "executions", "*", "events"}, serve: serveExecutionEvents},
	{method: http.MethodGet, shape: []string{"flows", "*", "aliases", "*", "executions", "*", "flowsnapshot"}, serve: serveExecutionSnapshot},
	{method: http.MethodPost, shape: []string{"flows", "*", "aliases", "*", "executions", "*", "stop"}, serve: serveStopExecution},
	{method: http.MethodGet, shape: []string{"flows", "*", "executions"}, serve: serveListExecutions},
}

// docContentShape is the GetDocumentContent path.
//
//nolint:gochecknoglobals // fixed path shape.
var docContentShape = []string{"knowledgebases", "*", "datasources", "*", "documents", "*", "content"}

// findRoute returns the route the request fits, or nil.
func findRoute(r *http.Request) *route {
	segs := segments(r)

	for i := range routes {
		rt := &routes[i]
		if rt.method != r.Method || !fitsShape(rt.shape, segs) {
			continue
		}

		if rt.noTrailingSlash && strings.HasSuffix(r.URL.Path, "/") {
			continue
		}

		if rt.query != "" && r.URL.Query().Get(rt.query) == "" {
			continue
		}

		return rt
	}

	return nil
}

func fitsShape(shape, segs []string) bool {
	if len(shape) != len(segs) {
		return false
	}

	for i, s := range shape {
		if s != "*" && s != segs[i] {
			return false
		}
	}

	return true
}

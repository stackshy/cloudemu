package cloudwatch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// IAMChecks names the IAM action of a request from the operation dispatch
// will run, picked by the same cloudwatchOp ServeHTTP uses for all three
// protocols. A CBOR request with a query-string Action, or a JSON target
// alongside a form Action, is therefore authorized as the operation that
// actually executes. An unknown operation is authorized as such and then
// answered with an error, so nothing runs.
func (h *Handler) IAMChecks(r *http.Request, _ awsauthz.Scope) ([]awsauthz.Check, bool) {
	decodeRequestBody(r)

	op, _, err := cloudwatchOp(r)
	if err != nil || op == "" {
		return nil, false
	}

	return awsauthz.Single(h.IAMService()+":"+op, ""), true
}

// WriteAccessDenied writes the 403 in the protocol of the request: the query
// XML error, the awsJson1_0 error, or the rpc-v2-cbor error.
func (*Handler) WriteAccessDenied(w http.ResponseWriter, r *http.Request, msg string) {
	_, proto, _ := cloudwatchOp(r)

	switch proto {
	case protoQuery:
		writeQueryError(w, http.StatusForbidden, "AccessDenied", msg)
	case protoJSON:
		(&jsonWriter{w: w}).writeError(http.StatusForbidden, "AccessDeniedException", msg)
	case protoCBOR:
		writeCBORError(w, http.StatusForbidden, "AccessDeniedException", msg)
	}
}

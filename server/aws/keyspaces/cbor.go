package keyspaces

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// Smithy RPC v2 CBOR. aws-sdk-go-v2/service/keyspaces v1.37.0 added it beside
// AWS JSON 1.0 and prefers it, so hashicorp/aws 6.69+ sends every Keyspaces
// call as
//
//	POST /service/KeyspacesService/operation/<Operation>
//	Smithy-Protocol: rpc-v2-cbor
//	Content-Type:    application/cbor
//
// with a CBOR body that uses the same member names as the JSON protocol.
const (
	smithyProtocolHeader = "Smithy-Protocol"
	rpcV2CBOR            = "rpc-v2-cbor"
	cborContentType      = "application/cbor"
	cborPathPrefix       = "/service/KeyspacesService/operation/"
	jsonContentType      = "application/x-amz-json-1.0"
	maxCBORBodyBytes     = 1 << 20
)

var errNotCBORMap = errors.New("request body must be a CBOR map")

// timestampMembers are the Timestamp members of the Keyspaces model. JSON
// carries them as epoch-second numbers; rpc-v2-cbor requires CBOR tag 1, and
// the SDK rejects an untagged number.
//
//nolint:gochecknoglobals // static protocol lookup table
var timestampMembers = map[string]bool{
	"creationTimestamp":                  true,
	"lastModifiedTimestamp":              true,
	"earliestRestorableTimestamp":        true,
	"lastUpdateToPayPerRequestTimestamp": true,
}

//nolint:gochecknoglobals // reused codec modes
var (
	cborDecMode = mustCBORDecMode()
	cborEncMode = mustCBOREncMode()
)

func mustCBORDecMode() cbor.DecMode {
	mode, err := cbor.DecOptions{DefaultMapType: reflect.TypeOf(map[string]any(nil))}.DecMode()
	if err != nil {
		panic(err)
	}

	return mode
}

// mustCBOREncMode writes time.Time as tag 1 over epoch seconds, the Smithy
// timestamp encoding.
func mustCBOREncMode() cbor.EncMode {
	mode, err := cbor.EncOptions{Time: cbor.TimeUnixDynamic, TimeTag: cbor.EncTagRequired}.EncMode()
	if err != nil {
		panic(err)
	}

	return mode
}

// CBORHandler serves the rpc-v2-cbor form of the Keyspaces API. It only
// converts the wire format: each request is re-encoded as AWS JSON 1.0 and run
// through the JSON Handler, and the answer is re-encoded as CBOR, so both
// protocols share one dispatch and one set of business rules.
type CBORHandler struct {
	json *Handler
}

// NewCBOR returns an rpc-v2-cbor Keyspaces handler backed by db.
func NewCBOR(db ksdriver.Keyspaces) *CBORHandler {
	return &CBORHandler{json: New(db)}
}

// Matches claims rpc-v2-cbor requests addressed to the KeyspacesService.
func (*CBORHandler) Matches(r *http.Request) bool {
	return r.Header.Get(smithyProtocolHeader) == rpcV2CBOR && strings.HasPrefix(r.URL.Path, cborPathPrefix)
}

// cborOperation returns the operation named in the URL path, or "".
func cborOperation(r *http.Request) string {
	op := strings.TrimPrefix(r.URL.Path, cborPathPrefix)
	if strings.Contains(op, "/") {
		return ""
	}

	return op
}

// ServeHTTP converts the CBOR request to JSON, runs the JSON handler, and
// converts its response back to CBOR.
func (h *CBORHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := cborOperation(r)
	if op == "" {
		writeCBORError(w, http.StatusBadRequest, "ValidationException", "missing operation in path")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCBORBodyBytes))
	if err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", "cannot read body: "+err.Error())
		return
	}

	body, err := cborToJSON(raw)
	if err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", "invalid CBOR: "+err.Error())
		return
	}

	jr := r.Clone(r.Context())
	jr.Body = io.NopCloser(bytes.NewReader(body))
	jr.ContentLength = int64(len(body))
	jr.Header.Set("X-Amz-Target", targetPrefix+op)
	jr.Header.Set("Content-Type", jsonContentType)

	rec := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	h.json.ServeHTTP(rec, jr)

	writeCBORFromJSON(w, rec)
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*CBORHandler) IAMService() string { return iamService }

// IAMChecks names the IAM action of a request from the operation in the URL
// path, the same value ServeHTTP dispatches on. The resource is unknown, as for
// the JSON protocol.
func (h *CBORHandler) IAMChecks(r *http.Request, _ awsauthz.Scope) ([]awsauthz.Check, bool) {
	op := cborOperation(r)
	if op == "" {
		return nil, false
	}

	return awsauthz.Single(h.IAMService()+":"+op, ""), true
}

// WriteAccessDenied writes the authorization 403 as an rpc-v2-cbor error.
func (*CBORHandler) WriteAccessDenied(w http.ResponseWriter, _ *http.Request, msg string) {
	writeCBORError(w, http.StatusForbidden, "AccessDeniedException", msg)
}

// bufferedResponse captures the JSON handler's response for re-encoding.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }

// writeCBORFromJSON re-encodes a captured JSON response as rpc-v2-cbor. An
// error keeps its status and carries __type and message, as the Smithy
// protocol expects.
func writeCBORFromJSON(w http.ResponseWriter, rec *bufferedResponse) {
	dec := json.NewDecoder(&rec.body)
	dec.UseNumber()

	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		writeCBORError(w, http.StatusInternalServerError, "InternalServerException", "cannot re-encode response: "+err.Error())
		return
	}

	if rec.status >= http.StatusBadRequest {
		obj, _ := decoded.(map[string]any)
		errType, _ := obj["__type"].(string)
		msg, _ := obj["Message"].(string)
		writeCBORError(w, rec.status, errType, msg)

		return
	}

	payload, err := jsonValueToCBOR("", decoded)
	if err != nil {
		writeCBORError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
		return
	}

	writeCBOR(w, http.StatusOK, payload)
}

func writeCBORError(w http.ResponseWriter, status int, errType, msg string) {
	writeCBOR(w, status, map[string]any{"__type": errType, "message": msg})
}

func writeCBOR(w http.ResponseWriter, status int, payload any) {
	body, err := cborEncMode.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		body, _ = cborEncMode.Marshal(map[string]any{"__type": "InternalServerException", "message": err.Error()})
	}

	w.Header().Set(smithyProtocolHeader, rpcV2CBOR)
	w.Header().Set("Content-Type", cborContentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// jsonValueToCBOR converts a decoded JSON response value for CBOR encoding:
// integral numbers become integers (the SDK reads Integer and Long members
// only from CBOR integers), other numbers float64, and the Timestamp members
// time.Time so they encode with tag 1. key is the member name v belongs to.
func jsonValueToCBOR(key string, v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, e := range t {
			c, err := jsonValueToCBOR(k, e)
			if err != nil {
				return nil, err
			}

			out[k] = c
		}

		return out, nil
	case []any:
		out := make([]any, len(t))

		for i, e := range t {
			c, err := jsonValueToCBOR(key, e)
			if err != nil {
				return nil, err
			}

			out[i] = c
		}

		return out, nil
	case json.Number:
		return jsonNumberToCBOR(key, t)
	default:
		return v, nil
	}
}

func jsonNumberToCBOR(key string, n json.Number) (any, error) {
	if timestampMembers[key] {
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("member %s: %w", key, err)
		}

		sec, frac := math.Modf(f)

		return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(), nil
	}

	if i, err := n.Int64(); err == nil {
		return i, nil
	}

	f, err := n.Float64()
	if err != nil {
		return nil, fmt.Errorf("member %s: %w", key, err)
	}

	return f, nil
}

// cborToJSON re-encodes a CBOR request body as AWS JSON 1.0: a tag-1
// timestamp becomes epoch seconds and a byte string base64, the JSON forms of
// those Smithy types. An empty body is an empty input structure.
func cborToJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}

	var v any
	if err := cborDecMode.Unmarshal(raw, &v); err != nil {
		return nil, err
	}

	if _, ok := v.(map[string]any); !ok {
		return nil, fmt.Errorf("%w, got %T", errNotCBORMap, v)
	}

	return json.Marshal(cborValueToJSON(v))
}

func cborValueToJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = cborValueToJSON(e)
		}

		return t
	case []any:
		for i, e := range t {
			t[i] = cborValueToJSON(e)
		}

		return t
	case time.Time:
		return float64(t.UnixNano()) / float64(time.Second)
	case []byte:
		return base64.StdEncoding.EncodeToString(t)
	default:
		return v
	}
}

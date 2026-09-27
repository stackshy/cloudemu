package cloudwatch

// CloudWatch over awsJson1_0. botocore 1.43+ (boto3 and the Python aws CLI)
// picks this protocol for CloudWatch: a POST with
//
//	X-Amz-Target: GraniteServiceVersion20100801.<Operation>
//	Content-Type: application/x-amz-json-1.0
//
// The JSON and CBOR wire shapes use the same member names. The two protocols
// only differ in how timestamps (epoch seconds numbers in JSON, CBOR tag 1)
// and blobs (base64 strings in JSON, CBOR byte strings) are written, and in
// the error envelope. So the JSON codec converts the request body to CBOR,
// runs the same per-op handler and core as rpc-v2-cbor, and converts the
// result back to JSON on the way out.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
)

const (
	jsonTargetPrefix = "GraniteServiceVersion20100801."
	jsonContentType  = "application/x-amz-json-1.0"
	jsonTargetHeader = "X-Amz-Target"
	queryErrorHeader = "X-Amzn-Query-Error"
	errTypeHeader    = "X-Amzn-Errortype"
	errSerialization = "SerializationException"
	msPerSecond      = 1e3
)

var (
	errNotJSONObject = errors.New("request body must be a JSON object")
	errTrailingJSON  = errors.New("invalid JSON: unexpected data after the top-level object")
)

// jsonErrorShapes maps the query error codes the cores return to the error
// shape names of the CloudWatch model. CloudWatch is awsQueryCompatible, so
// the shape name goes in __type and the query code rides in the
// X-Amzn-Query-Error header, which botocore uses as the error code.
//
//nolint:gochecknoglobals // read-only lookup table
var jsonErrorShapes = map[string]string{
	"InvalidParameterValue":       "InvalidParameterValueException",
	"InvalidParameterCombination": "InvalidParameterCombinationException",
	"MissingParameter":            "MissingRequiredParameterException",
	"ValidationError":             "ValidationException",
	"InvalidFormat":               "InvalidFormatFault",
	"LimitExceeded":               "LimitExceededFault",
	"InternalServiceError":        "InternalServiceFault",
	"InvalidParameterInput":       "DashboardInvalidInputError",
}

// isJSONRequest reports whether r is a CloudWatch awsJson1_0 request.
func isJSONRequest(r *http.Request) bool {
	return r.Method == http.MethodPost && jsonOperation(r) != ""
}

// jsonOperation returns the operation named by the X-Amz-Target header, or ""
// when the target is not a CloudWatch one.
func jsonOperation(r *http.Request) string {
	op, ok := strings.CutPrefix(r.Header.Get(jsonTargetHeader), jsonTargetPrefix)
	if !ok {
		return ""
	}

	return op
}

// serveJSON handles a CloudWatch awsJson1_0 request.
func (h *Handler) serveJSON(w http.ResponseWriter, r *http.Request) {
	jw := &jsonWriter{w: w}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		jw.writeError(http.StatusBadRequest, errSerialization, err.Error())
		return
	}

	body, err := jsonToCBOR(raw)
	if err != nil {
		jw.writeError(http.StatusBadRequest, errSerialization, err.Error())
		return
	}

	h.dispatch(jw, r, jsonOperation(r), body)
}

// jsonWriter is the http.ResponseWriter the per-op handlers get for a JSON
// request. writeCBORResponse and writeCBORError hand their payload to it, so
// every op answers in JSON without a JSON twin of its handler.
type jsonWriter struct {
	w http.ResponseWriter
}

func (j *jsonWriter) Header() http.Header         { return j.w.Header() }
func (j *jsonWriter) Write(b []byte) (int, error) { return j.w.Write(b) }
func (j *jsonWriter) WriteHeader(status int)      { j.w.WriteHeader(status) }

// writeResult encodes a CBOR wire struct as a JSON response body.
func (j *jsonWriter) writeResult(payload any) {
	body, err := cborPayloadToJSON(payload)
	if err != nil {
		j.writeError(http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	j.w.Header().Set("Content-Type", jsonContentType)
	j.w.WriteHeader(http.StatusOK)
	_, _ = j.w.Write(body)
}

// writeError writes an awsJson1_0 error: the model shape name in __type and
// X-Amzn-Errortype, and the query code in X-Amzn-Query-Error.
func (j *jsonWriter) writeError(status int, code, msg string) {
	shape := code
	if s, ok := jsonErrorShapes[code]; ok {
		shape = s
	}

	fault := "Sender"
	if status >= http.StatusInternalServerError {
		fault = "Receiver"
	}

	body, _ := json.Marshal(map[string]string{"__type": shape, "message": msg})

	hdr := j.w.Header()
	hdr.Set("Content-Type", jsonContentType)
	hdr.Set(errTypeHeader, shape)
	hdr.Set(queryErrorHeader, code+";"+fault)
	j.w.WriteHeader(status)
	_, _ = j.w.Write(body)
}

// jsonToCBOR re-encodes a JSON request body as CBOR for the shared decoders.
// Integral numbers (60, 60.0, 6e1) become CBOR integers so they decode into
// int fields as well as float ones, and timestamps (epoch seconds numbers)
// decode into time.Time as untagged numbers. An empty body is an empty input,
// and anything after the JSON object is rejected.
func jsonToCBOR(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if _, ok := v.(map[string]any); !ok {
		return nil, errNotJSONObject
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailingJSON
	}

	conv, err := jsonNumbersToCBOR(v)
	if err != nil {
		return nil, err
	}

	return cbor.Marshal(conv)
}

func jsonNumbersToCBOR(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			c, err := jsonNumbersToCBOR(e)
			if err != nil {
				return nil, err
			}

			t[k] = c
		}
	case []any:
		for i, e := range t {
			c, err := jsonNumbersToCBOR(e)
			if err != nil {
				return nil, err
			}

			t[i] = c
		}
	case json.Number:
		return jsonNumberToCBOR(t)
	}

	return v, nil
}

// jsonNumberToCBOR returns an int64 for an integral number in int64 range and
// a float64 otherwise.
func jsonNumberToCBOR(n json.Number) (any, error) {
	if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		return i, nil
	}

	f, err := n.Float64()
	if err != nil {
		return nil, fmt.Errorf("invalid number %q: %w", n, err)
	}

	if f == math.Trunc(f) && f >= math.MinInt64 && f < math.MaxInt64 {
		return int64(f), nil
	}

	return f, nil
}

// cborDecMode decodes a CBOR response back to generic values, with string map
// keys and tag 1 timestamps as time.Time.
var cborDecMode = mustCBORDecMode() //nolint:gochecknoglobals // reused decoder

func mustCBORDecMode() cbor.DecMode {
	mode, err := cbor.DecOptions{DefaultMapType: reflect.TypeFor[map[string]any]()}.DecMode()
	if err != nil {
		panic(err)
	}

	return mode
}

// cborPayloadToJSON encodes a CBOR wire struct as JSON. It goes through the
// CBOR encoding so the cbor struct tags (names, omitempty) stay the single
// definition of the wire shape.
func cborPayloadToJSON(payload any) ([]byte, error) {
	enc, err := smithyEncMode.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var v any
	if err := cborDecMode.Unmarshal(enc, &v); err != nil {
		return nil, err
	}

	return json.Marshal(cborToJSONValue(v))
}

// cborToJSONValue converts decoded CBOR values to their awsJson1_0 form:
// timestamps as epoch seconds, byte strings as base64 (encoding/json does
// that for []byte), and null members dropped.
func cborToJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, e := range t {
			if e == nil {
				continue
			}

			out[k] = cborToJSONValue(e)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cborToJSONValue(e)
		}

		return out
	case time.Time:
		return epochSeconds(t)
	case float64:
		return jsonDouble(t)
	}

	return v
}

// jsonDouble keeps a double a double on the wire: 20 is written as 20.0, so
// botocore hands back a float like it does for real CloudWatch. CBOR keeps
// int and double members apart, so only double members get here.
func jsonDouble(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}

	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}

	return json.Number(s)
}

// epochSeconds renders t as the JSON number of seconds since the epoch, with
// millisecond precision like the AWS SDKs.
func epochSeconds(t time.Time) json.Number {
	return json.Number(strconv.FormatFloat(float64(t.UnixMilli())/msPerSecond, 'f', -1, 64))
}

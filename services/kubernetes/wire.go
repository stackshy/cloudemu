package kubernetes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// contentTypeJSON is the Kubernetes API server's default content type for
// both requests and responses.
const contentTypeJSON = "application/json"

// writeJSON marshals v and writes it as a JSON response with the given
// status code. The Kubernetes Content-Type is always application/json.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

// writeStatus writes a metav1.Status response with the given code, reason,
// and message — matching what a real apiserver returns for errors. client-go
// decodes these as typed errors (kerrors.IsNotFound, IsAlreadyExists, etc.).
func writeStatus(w http.ResponseWriter, code int, reason metav1.StatusReason, message string) {
	writeJSON(w, code, &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Code:     int32(code), //nolint:gosec // HTTP status codes fit in int32 trivially.
		Reason:   reason,
		Message:  message,
	})
}

// writeNotFound emits a 404 Status response.
func writeNotFound(w http.ResponseWriter, message string) {
	writeStatus(w, http.StatusNotFound, metav1.StatusReasonNotFound, message)
}

// writeAlreadyExists emits a 409 Status response.
func writeAlreadyExists(w http.ResponseWriter, message string) {
	writeStatus(w, http.StatusConflict, metav1.StatusReasonAlreadyExists, message)
}

// writeBadRequest emits a 400 Status response.
func writeBadRequest(w http.ResponseWriter, message string) {
	writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest, message)
}

// writeMethodNotAllowed emits a 405 Status response.
func writeMethodNotAllowed(w http.ResponseWriter, message string) {
	writeStatus(w, http.StatusMethodNotAllowed, metav1.StatusReasonMethodNotAllowed, message)
}

// readJSON decodes the request body into v. Returns false (and writes a 400
// Status response) if decoding fails so callers can early-return.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeBadRequest(w, "k8s api: read body: "+err.Error())

		return false
	}

	// Kubernetes protobuf frames begin with the magic prefix "k8s\x00".
	// kubectl negotiates protobuf for built-in types once discovery advertises
	// them, so this path became reachable the moment discovery landed — and a
	// JSON decoder reports it as `invalid character 'k'`, which reads like a
	// malformed request rather than an encoding the server does not speak.
	//
	// 415 is the answer the Kubernetes client libraries are built to handle:
	// they retry the same request as JSON. Returning 400 instead makes kubectl
	// give up, which is why writes failed while reads succeeded.
	if bytes.HasPrefix(body, protobufMagic) {
		writeStatus(w, http.StatusUnsupportedMediaType, metav1.StatusReasonUnsupportedMediaType,
			"k8s api: protobuf encoding is not supported; retry with application/json")

		return false
	}

	if err := json.Unmarshal(body, v); err != nil {
		writeBadRequest(w, "k8s api: decode body: "+err.Error())

		return false
	}

	return true
}

// protobufMagic is the 4-byte prefix on every Kubernetes protobuf frame
// (k8s.io/apimachinery/pkg/runtime.protoEncodingPrefix).
var protobufMagic = []byte{'k', '8', 's', 0x00}

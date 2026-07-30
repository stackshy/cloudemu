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
	// kubectl and client-go send built-in kinds as protobuf on writes (their
	// Accept header still allows JSON, which is why reads worked while writes
	// arrived protobuf-framed). Decode them rather than rejecting — kubectl does
	// NOT retry a write as JSON on 415, it surfaces the error, so a 415 here
	// means `kubectl create/scale/apply` simply cannot write to the emulator.
	if bytes.HasPrefix(body, protobufMagic) {
		return decodeProtobufBody(w, body, v)
	}

	if err := json.Unmarshal(body, v); err != nil {
		writeBadRequest(w, "k8s api: decode body: "+err.Error())

		return false
	}

	return true
}

// protobufMagic is the 4-byte prefix on every Kubernetes protobuf frame
// (k8s.io/apimachinery/pkg/runtime.protoEncodingPrefix).
//
//nolint:gochecknoglobals // fixed protocol constant; []byte can't be a const.
var protobufMagic = []byte{'k', '8', 's', 0x00}

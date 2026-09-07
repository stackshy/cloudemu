// Package kinesisvideo implements the Amazon Kinesis Video Streams control-plane
// API (restJson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/kinesisvideo client (or the `aws kinesisvideo` CLI, or
// the aws_kinesis_video_stream Terraform resource) at a Server registered with
// this handler and the stream, signaling-channel and tagging operations work
// end-to-end against an in-memory driver.
//
// Kinesis Video routes by POST to a per-operation action path (POST
// /createStream, POST /describeStream, POST /createSignalingChannel, …); there
// is no X-Amz-Target header. Matches claims those action paths, which run before
// the S3 catch-all. The stream-specific and channel-specific paths are unique to
// Kinesis Video, but the resource-level tagging paths (/TagResource,
// /UntagResource, /ListTagsForResource) are also claimed by Savings Plans, so
// this handler scopes those three to a request body carrying a Kinesis Video
// (:kinesisvideo:) ARN and must register before the Savings Plans handler; a
// non-Kinesis-Video ARN falls through to it.
//
// This is a control-plane-only surface: the GetMedia/PutMedia data plane is out
// of scope. Distinct from the kinesis package (Kinesis Data Streams).
package kinesisvideo

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// Action paths unique to Kinesis Video.
const (
	pathCreateStream        = "/createStream"
	pathDescribeStream      = "/describeStream"
	pathUpdateStream        = "/updateStream"
	pathUpdateDataRetention = "/updateDataRetention"
	pathDeleteStream        = "/deleteStream"
	pathListStreams         = "/listStreams"
	pathTagStream           = "/tagStream"
	pathUntagStream         = "/untagStream"
	pathListTagsForStream   = "/listTagsForStream"

	pathCreateChannel   = "/createSignalingChannel"
	pathDescribeChannel = "/describeSignalingChannel"
	pathUpdateChannel   = "/updateSignalingChannel"
	pathDeleteChannel   = "/deleteSignalingChannel"
	pathListChannels    = "/listSignalingChannels"
)

// Resource-level tagging action paths, shared with Savings Plans and therefore
// scoped by a Kinesis Video ARN in the request body.
const (
	pathTagResource         = "/TagResource"
	pathUntagResource       = "/UntagResource"
	pathListTagsForResource = "/ListTagsForResource"
)

// arnMarker scopes the shared resource-level tagging paths to Kinesis Video
// ARNs.
const arnMarker = ":kinesisvideo:"

// peekBodyBytes bounds the body read performed by Matches when scoping a shared
// tagging path by its ARN.
const peekBodyBytes = 1 << 16

// Handler serves Amazon Kinesis Video Streams requests against a driver.
type Handler struct {
	kv     driver.KinesisVideo
	routes map[string]http.HandlerFunc
}

// New returns a Kinesis Video handler backed by d.
func New(d driver.KinesisVideo) *Handler {
	h := &Handler{kv: d}
	h.routes = map[string]http.HandlerFunc{
		pathCreateStream:        h.createStream,
		pathDescribeStream:      h.describeStream,
		pathUpdateStream:        h.updateStream,
		pathUpdateDataRetention: h.updateDataRetention,
		pathDeleteStream:        h.deleteStream,
		pathListStreams:         h.listStreams,
		pathTagStream:           h.tagStream,
		pathUntagStream:         h.untagStream,
		pathListTagsForStream:   h.listTagsForStream,

		pathCreateChannel:   h.createSignalingChannel,
		pathDescribeChannel: h.describeSignalingChannel,
		pathUpdateChannel:   h.updateSignalingChannel,
		pathDeleteChannel:   h.deleteSignalingChannel,
		pathListChannels:    h.listSignalingChannels,

		pathTagResource:         h.tagResource,
		pathUntagResource:       h.untagResource,
		pathListTagsForResource: h.listTagsForResource,
	}

	return h
}

// isSharedTagPath reports whether path is one of the resource-level tagging
// paths shared with Savings Plans.
func isSharedTagPath(path string) bool {
	switch path {
	case pathTagResource, pathUntagResource, pathListTagsForResource:
		return true
	default:
		return false
	}
}

// Matches claims a POST to a Kinesis Video action path. The stream and channel
// paths are unique, so they match on path alone; the three resource-level
// tagging paths are shared with Savings Plans, so they match only when the
// request body carries a Kinesis Video ARN.
func (h *Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}

	if _, ok := h.routes[r.URL.Path]; !ok {
		return false
	}

	if isSharedTagPath(r.URL.Path) {
		return strings.Contains(peekResourceARN(r), arnMarker)
	}

	return true
}

// ServeHTTP dispatches on the request path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if fn, ok := h.routes[r.URL.Path]; ok {
		fn(w, r)

		return
	}

	notFoundPath(w, r.URL.Path)
}

// peekResourceARN reads the request body without consuming it (the body is
// restored for ServeHTTP and for any downstream handler) and returns its
// ResourceARN field, so Matches can scope a shared tagging path to Kinesis
// Video.
func peekResourceARN(r *http.Request) string {
	if r.Body == nil {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, peekBodyBytes))
	_ = r.Body.Close()

	r.Body = io.NopCloser(bytes.NewReader(body))

	if err != nil {
		return ""
	}

	var probe struct {
		ResourceARN string `json:"ResourceARN"`
	}

	if json.Unmarshal(body, &probe) != nil {
		return ""
	}

	return probe.ResourceARN
}

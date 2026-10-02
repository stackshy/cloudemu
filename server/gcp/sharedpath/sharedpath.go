// Package sharedpath carries an optional API hint for GCP REST paths that more
// than one service serves on the emulator's single port.
//
// Real GCP tells these services apart by host: GKE, AlloyDB and Managed Kafka
// all serve /v1/projects/{p}/locations/{l}/clusters, on container, alloydb and
// managedkafka.googleapis.com. A client pointed at the emulator sends the
// emulator's own Host, so the hint is opt-in and comes from either:
//
//   - the Host header, <api>.googleapis.com (also <api>.mtls.googleapis.com,
//     <region>-<api>.googleapis.com and <api>.localhost), or
//   - a path alias whose first segment is the googleapis host, for example
//     http://localhost:4569/alloydb.googleapis.com/v1/projects/... Rewrite
//     strips that segment before dispatch and records the label.
//
// Only labels in a fixed set are honored, so an unknown label never makes a
// handler yield.
package sharedpath

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// Labels are the googleapis host labels of the services that share a REST path
// with another service.
const (
	AlloyDB             = "alloydb"
	Container           = "container"
	ManagedKafka        = "managedkafka"
	Spanner             = "spanner"
	SQLAdmin            = "sqladmin"
	AIPlatform          = "aiplatform"
	IntrusionDetection  = "ids"
	File                = "file"
	Redis               = "redis"
	SecureSourceManager = "securesourcemanager"
	DataFusion          = "datafusion"
	GKEBackup           = "gkebackup"
	BackupDR            = "backupdr"
	Dataform            = "dataform"
	ArtifactRegistry    = "artifactregistry"
)

const (
	googleapisSuffix = ".googleapis.com"
	localhostSuffix  = ".localhost"
	mtlsSuffix       = ".mtls"
)

type ctxKey struct{}

// API returns the hinted label: the one Rewrite recorded from the path alias,
// else the one in the Host header. It returns "" when neither names a known
// label.
func API(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKey{}).(string); ok {
		return v
	}

	return hostLabel(r.Host)
}

// Is reports whether the request is hinted for mine.
func Is(r *http.Request, mine string) bool {
	return API(r) == mine
}

// Yield reports whether the hint names a member of group other than mine. A
// hint for an API outside the group is ignored.
func Yield(r *http.Request, mine string, group ...string) bool {
	api := API(r)
	if api == "" || api == mine {
		return false
	}

	for _, g := range group {
		if g == api {
			return true
		}
	}

	return false
}

// Rewrite is a server pre-dispatch hook. When the first path segment ends in
// ".googleapis.com" it strips that segment from the path, records the label
// when it is known, and returns the replacement request. Any other path is
// returned unchanged. It never stops dispatch.
func Rewrite(_ http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	host, rest, ok := splitAlias(r.URL.Path)
	if !ok {
		return r, true
	}

	ctx := r.Context()
	if label := hostLabel(host); label != "" {
		ctx = context.WithValue(ctx, ctxKey{}, label)
	}

	out := r.WithContext(ctx)
	u := *r.URL
	u.Path = rest

	if u.RawPath != "" {
		if _, rawRest, rawOK := splitAlias(u.RawPath); rawOK {
			u.RawPath = rawRest
		} else {
			u.RawPath = ""
		}
	}

	out.URL = &u

	return out, true
}

// splitAlias splits "/<host>.googleapis.com/rest" into the host and "/rest".
// It reports false for any path whose first segment is not a googleapis host.
func splitAlias(path string) (host, rest string, ok bool) {
	if len(path) < len(googleapisSuffix)+1 || path[0] != '/' {
		return "", "", false
	}

	seg := path[1:]
	rest = "/"

	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg, rest = seg[:i], seg[i:]
	}

	if len(seg) <= len(googleapisSuffix) || !strings.HasSuffix(strings.ToLower(seg), googleapisSuffix) {
		return "", "", false
	}

	return seg, rest, true
}

// hostLabel maps a host (with or without port) to a known label, or "".
func hostLabel(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}

	h = strings.ToLower(h)

	switch {
	case strings.HasSuffix(h, googleapisSuffix):
		h = strings.TrimSuffix(h, googleapisSuffix)
	case strings.HasSuffix(h, localhostSuffix):
		h = strings.TrimSuffix(h, localhostSuffix)
	default:
		return ""
	}

	h = strings.TrimSuffix(h, mtlsSuffix)

	if known(h) {
		return h
	}

	// <region>-<api>, e.g. us-central1-aiplatform.
	if i := strings.LastIndexByte(h, '-'); i >= 0 && known(h[i+1:]) {
		return h[i+1:]
	}

	return ""
}

// IsZone reports whether location is a zone such as us-central1-a, as opposed
// to a region (us-central1) or the "-" wildcard.
func IsZone(location string) bool {
	i := strings.LastIndexByte(location, '-')
	if i <= 0 || len(location)-i != 2 || !strings.Contains(location[:i], "-") {
		return false
	}

	c := location[i+1]

	return c >= 'a' && c <= 'z' && location[i-1] >= '0' && location[i-1] <= '9'
}

func known(label string) bool {
	switch label {
	case AlloyDB, Container, ManagedKafka, Spanner, SQLAdmin, AIPlatform, IntrusionDetection,
		File, Redis, SecureSourceManager, DataFusion, GKEBackup, BackupDR, Dataform, ArtifactRegistry:
		return true
	default:
		return false
	}
}

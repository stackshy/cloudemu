// Internal tests for unexported helpers — route parsing, JSON-merge-patch
// semantics, resource-version bumping, and the wire-level status helpers
// that don't get hit by the external HTTP tests.

package kubernetes

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name string
		path string
		want *Route
	}{
		{
			name: "core_collection",
			path: "/api/v1/namespaces",
			want: &Route{APIGroup: "", APIVersion: "v1", Resource: "namespaces"},
		},
		{
			name: "core_item",
			path: "/api/v1/namespaces/demo",
			want: &Route{APIGroup: "", APIVersion: "v1", Resource: "namespaces", Name: "demo"},
		},
		{
			name: "core_namespaced_collection",
			path: "/api/v1/namespaces/demo/configmaps",
			want: &Route{APIGroup: "", APIVersion: "v1", Namespace: "demo", Resource: "configmaps"},
		},
		{
			name: "core_namespaced_item",
			path: "/api/v1/namespaces/demo/configmaps/settings",
			want: &Route{APIGroup: "", APIVersion: "v1", Namespace: "demo", Resource: "configmaps", Name: "settings"},
		},
		{
			name: "group_collection",
			path: "/apis/apps/v1/deployments",
			want: &Route{APIGroup: "apps", APIVersion: "v1", Resource: "deployments"},
		},
		{
			name: "group_namespaced_item",
			path: "/apis/apps/v1/namespaces/demo/deployments/web",
			want: &Route{APIGroup: "apps", APIVersion: "v1", Namespace: "demo", Resource: "deployments", Name: "web"},
		},
		{name: "empty", path: "/", want: nil},
		{name: "unknown_prefix", path: "/foo/v1/bar", want: nil},
		{name: "too_short_core", path: "/api", want: nil},
		{name: "too_short_group", path: "/apis/apps", want: nil},
		// Reaches parseCoreRoute() but parts after the "api" segment is just
		// the version — too short for fillResourceRoute, so parseCoreRoute
		// returns nil from its own early-return.
		{name: "core_version_only", path: "/api/v1", want: nil},
		// Reaches parseGroupRoute() but only carries group+version, no resource.
		{name: "group_version_only", path: "/apis/apps/v1", want: nil},
		{
			name: "namespaced_collection_missing_namespaces_segment",
			path: "/api/v1/notnamespaces/x/configmaps",
			want: nil,
		},
		{
			name: "namespaced_item_missing_namespaces_segment",
			path: "/api/v1/notnamespaces/x/configmaps/y",
			want: nil,
		},
		{name: "trailing_garbage", path: "/api/v1/a/b/c/d/e", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRoute(tt.path)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseRoute(%q) = %+v, want %+v", tt.path, got, tt.want)
			}
		})
	}
}

func TestSplitPath(t *testing.T) {
	cases := map[string][]string{
		"":               nil,
		"/":              nil,
		"a":              {"a"},
		"/a/b/c":         {"a", "b", "c"},
		"a/b/c/":         {"a", "b", "c"},
		"///foo///bar//": {"foo", "", "", "bar"}, // Trim() strips outer slashes; inner repeated slashes survive as empty segments.
	}

	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := splitPath(in)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("splitPath(%q) = %v, want %v", in, got, want)
			}
		})
	}
}

func TestMergeRFC7396(t *testing.T) {
	tests := []struct {
		name   string
		target string
		patch  string
		want   string
	}{
		{
			name:   "shallow_overwrite",
			target: `{"a":1,"b":2}`,
			patch:  `{"b":99}`,
			want:   `{"a":1,"b":99}`,
		},
		{
			name:   "null_deletes_key",
			target: `{"a":1,"b":2}`,
			patch:  `{"b":null}`,
			want:   `{"a":1}`,
		},
		{
			name:   "nested_merge",
			target: `{"data":{"x":1,"y":2}}`,
			patch:  `{"data":{"y":99,"z":3}}`,
			want:   `{"data":{"x":1,"y":99,"z":3}}`,
		},
		{
			name:   "scalar_patch_replaces_target",
			target: `{"a":1}`,
			patch:  `"replaced"`,
			want:   `"replaced"`,
		},
		{
			name:   "patch_on_non_object_target",
			target: `42`,
			patch:  `{"a":1}`,
			want:   `{"a":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := mergePatch([]byte(tt.target), []byte(tt.patch))
			if err != nil {
				t.Fatalf("mergePatch: %v", err)
			}

			var got, want any
			_ = json.Unmarshal(out, &got)
			_ = json.Unmarshal([]byte(tt.want), &want)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s, want %s", out, tt.want)
			}
		})
	}
}

func TestMergePatch_BadJSONReturnsError(t *testing.T) {
	if _, err := mergePatch([]byte(`{bad`), []byte(`{}`)); err == nil {
		t.Fatal("expected error on bad target, got nil")
	}

	if _, err := mergePatch([]byte(`{}`), []byte(`{bad`)); err == nil {
		t.Fatal("expected error on bad patch, got nil")
	}
}

func TestBumpResourceVersion(t *testing.T) {
	cases := map[string]string{
		"":      "1",
		"foo":   "1", // non-numeric resets to 1
		"1":     "2",
		"99":    "100",
		"99999": "100000",
	}

	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := bumpResourceVersion(in); got != want {
				t.Fatalf("bumpResourceVersion(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// brokenReader always errors on Read — used to drive applyJSONPatch's
// io.ReadAll failure branch.
type brokenReader struct{}

func (*brokenReader) Read([]byte) (int, error) {
	return 0, errors.New("simulated body read failure")
}

func (*brokenReader) Close() error { return nil }

func TestApplyJSONPatch_BodyReadError(t *testing.T) {
	req := httptest.NewRequest("PATCH", "/", &brokenReader{})
	req.Header.Set("Content-Type", "application/merge-patch+json")
	w := httptest.NewRecorder()

	cur := &corev1.ConfigMap{}
	if _, ok := applyJSONPatch(w, req, cur); ok {
		t.Fatal("expected !ok on broken body")
	}

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestReadJSON_BodyReadError(t *testing.T) {
	req := httptest.NewRequest("POST", "/", &brokenReader{})
	w := httptest.NewRecorder()

	var dst struct{}
	if readJSON(w, req, &dst) {
		t.Fatal("expected false on broken body")
	}

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestApplyJSONPatch_BadContentType(t *testing.T) {
	req := httptest.NewRequest("PATCH", "/", io.NopCloser(io.LimitReader(nil, 0)))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	cur := &corev1.ConfigMap{}
	if _, ok := applyJSONPatch(w, req, cur); ok {
		t.Fatal("expected !ok on bad content-type")
	}

	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

func TestWriteHelpers_StatusShapes(t *testing.T) {
	tests := []struct {
		name string
		fn   func() (int, string)
	}{
		{
			"AlreadyExists",
			func() (int, string) {
				w := httptest.NewRecorder()
				writeAlreadyExists(w, "msg")

				return w.Code, w.Body.String()
			},
		},
		{
			"BadRequest",
			func() (int, string) {
				w := httptest.NewRecorder()
				writeBadRequest(w, "msg")

				return w.Code, w.Body.String()
			},
		},
		{
			"MethodNotAllowed",
			func() (int, string) {
				w := httptest.NewRecorder()
				writeMethodNotAllowed(w, "msg")

				return w.Code, w.Body.String()
			},
		},
	}

	wantCode := map[string]int{"AlreadyExists": 409, "BadRequest": 400, "MethodNotAllowed": 405}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := tt.fn()

			if code != wantCode[tt.name] {
				t.Fatalf("status code: got %d, want %d", code, wantCode[tt.name])
			}

			var decoded map[string]any
			if err := json.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatalf("body not JSON: %v", err)
			}

			if decoded["kind"] != "Status" || decoded["status"] != "Failure" {
				t.Fatalf("body missing Status envelope: %s", body)
			}
		})
	}
}

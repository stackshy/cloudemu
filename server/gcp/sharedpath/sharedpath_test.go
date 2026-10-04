package sharedpath

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostLabel(t *testing.T) {
	cases := []struct {
		host, want string
	}{
		{"alloydb.googleapis.com", AlloyDB},
		{"ALLOYDB.googleapis.com:443", AlloyDB},
		{"container.mtls.googleapis.com", Container},
		{"us-central1-aiplatform.googleapis.com", AIPlatform},
		{"redis.localhost:4569", Redis},
		{"storage.googleapis.com", ""},
		{"www.googleapis.com", ""},
		{"googleapis.com", ""},
		{"localhost:4569", ""},
		{"host.docker.internal:4569", ""},
		{"cloudemu:4569", ""},
		{"alloydb.example.com", ""},
		{"", ""},
	}

	for _, tc := range cases {
		if got := hostLabel(tc.host); got != tc.want {
			t.Errorf("hostLabel(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestRewrite(t *testing.T) {
	cases := []struct {
		name, target, wantPath, wantAPI string
	}{
		{"alias keeps version", "/alloydb.googleapis.com/v1/projects/p/locations/l/clusters", "/v1/projects/p/locations/l/clusters", AlloyDB},
		{"alias with query", "/managedkafka.googleapis.com/v1/projects/p/locations/l/clusters?pageSize=5", "/v1/projects/p/locations/l/clusters", ManagedKafka},
		{"regional vertex alias", "/us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/endpoints", "/v1/projects/p/locations/us-central1/endpoints", AIPlatform},
		{"unknown label strips without hint", "/storage.googleapis.com/storage/v1/b", "/storage/v1/b", ""},
		{"bare alias", "/redis.googleapis.com", "/", Redis},
		{"plain v1 untouched", "/v1/projects/p/locations/l/clusters", "/v1/projects/p/locations/l/clusters", ""},
		{"storage untouched", "/storage/v1/b", "/storage/v1/b", ""},
		{"upload untouched", "/upload/storage/v1/b/bkt/o", "/upload/storage/v1/b/bkt/o", ""},
		{"direct media untouched", "/b/o", "/b/o", ""},
		{"admin untouched", "/_cloudemu/snapshot", "/_cloudemu/snapshot", ""},
		{"suffix only segment untouched", "/.googleapis.com/v1", "/.googleapis.com/v1", ""},
		{"later segment untouched", "/v1/alloydb.googleapis.com/x", "/v1/alloydb.googleapis.com/x", ""},
		{"root untouched", "/", "/", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := httptest.NewRequest(http.MethodGet, tc.target, nil)
			before := in.URL.Path

			out, proceed := Rewrite(nil, in)
			if !proceed {
				t.Fatal("Rewrite stopped dispatch")
			}

			if out.URL.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", out.URL.Path, tc.wantPath)
			}

			if got := API(out); got != tc.wantAPI {
				t.Errorf("API = %q, want %q", got, tc.wantAPI)
			}

			if in.URL.Path != before {
				t.Errorf("input request mutated: %q", in.URL.Path)
			}

			if tc.wantPath == before && out != in {
				t.Error("non-alias path returned a new request")
			}
		})
	}
}

func TestRewriteRawPath(t *testing.T) {
	in := httptest.NewRequest(http.MethodGet, "/storage.googleapis.com/storage/v1/b/bkt/o/a%2Fb", nil)

	out, _ := Rewrite(nil, in)
	if out.URL.Path != "/storage/v1/b/bkt/o/a/b" || out.URL.RawPath != "/storage/v1/b/bkt/o/a%2Fb" {
		t.Fatalf("path=%q raw=%q", out.URL.Path, out.URL.RawPath)
	}
}

func TestYieldAndIs(t *testing.T) {
	hinted := func(host string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/v1/projects/p/locations/l/instances", nil)
		r.Host = host

		return r
	}

	cases := []struct {
		name  string
		r     *http.Request
		yield bool
		is    bool
	}{
		{"no hint", hinted("localhost:4569"), false, false},
		{"hint names me", hinted("file.googleapis.com"), false, true},
		{"hint names a group member", hinted("redis.googleapis.com"), true, false},
		{"hint outside the group", hinted("alloydb.googleapis.com"), false, false},
	}

	for _, tc := range cases {
		if got := Yield(tc.r, File, Redis, DataFusion, SecureSourceManager); got != tc.yield {
			t.Errorf("%s: Yield = %v, want %v", tc.name, got, tc.yield)
		}

		if got := Is(tc.r, File); got != tc.is {
			t.Errorf("%s: Is = %v, want %v", tc.name, got, tc.is)
		}
	}
}

func TestAliasHintWinsOverHost(t *testing.T) {
	in := httptest.NewRequest(http.MethodGet, "/redis.googleapis.com/v1/projects/p/locations/l/instances", nil)
	in.Host = "file.googleapis.com"

	out, _ := Rewrite(nil, in)
	if got := API(out); got != Redis {
		t.Fatalf("API = %q, want %q", got, Redis)
	}
}

func TestIsZone(t *testing.T) {
	cases := map[string]bool{
		"us-central1-a": true, "europe-west4-c": true,
		"us-central1": false, "-": false, "global": false, "us-a": false, "": false, "us-central1-ab": false,
	}

	for loc, want := range cases {
		if got := IsZone(loc); got != want {
			t.Errorf("IsZone(%q) = %v, want %v", loc, got, want)
		}
	}
}

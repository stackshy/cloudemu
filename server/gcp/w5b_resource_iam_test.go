package gcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// w5bCase is one collection that gained the google.iam.v1 resource verbs.
type w5bCase struct {
	name     string
	create   [][3]string // method, path, body
	resource string      // path of the resource, the verb is appended
	missing  string      // path of a resource that does not exist
	del      [2]string   // method, path
}

func w5bCases() []w5bCase {
	const (
		bq  = "/bigquery/v2/projects/demo/datasets"
		sp  = "/v1/projects/demo/instances"
		dns = "/dns/v1/projects/demo/managedZones"
		ps  = "/v1/projects/demo"
	)

	return []w5bCase{
		{
			name: "bigquery table",
			create: [][3]string{
				{http.MethodPost, bq, `{"datasetReference":{"datasetId":"ds1"}}`},
				{http.MethodPost, bq + "/ds1/tables", `{"tableReference":{"tableId":"t1"}}`},
			},
			resource: bq + "/ds1/tables/t1",
			missing:  bq + "/ds1/tables/nope",
			del:      [2]string{http.MethodDelete, bq + "/ds1/tables/t1"},
		},
		{
			name:     "bigquery dataset",
			create:   [][3]string{{http.MethodPost, bq, `{"datasetReference":{"datasetId":"ds2"}}`}},
			resource: bq + "/ds2",
			missing:  bq + "/nope",
			del:      [2]string{http.MethodDelete, bq + "/ds2"},
		},
		{
			name: "spanner instance",
			create: [][3]string{{http.MethodPost, sp,
				`{"instanceId":"si1","instance":{"config":"regional-us-central1","displayName":"si1","nodeCount":1}}`}},
			resource: sp + "/si1",
			missing:  sp + "/nope",
			del:      [2]string{http.MethodDelete, sp + "/si1"},
		},
		{
			name: "spanner database",
			create: [][3]string{
				{http.MethodPost, sp,
					`{"instanceId":"si2","instance":{"config":"regional-us-central1","displayName":"si2","nodeCount":1}}`},
				{http.MethodPost, sp + "/si2/databases", "{\"createStatement\":\"CREATE DATABASE `db1`\"}"},
			},
			resource: sp + "/si2/databases/db1",
			missing:  sp + "/si2/databases/nope",
			del:      [2]string{http.MethodDelete, sp + "/si2/databases/db1"},
		},
		{
			name: "dns managed zone",
			create: [][3]string{{http.MethodPost, dns,
				`{"name":"z1","dnsName":"example.com.","description":"d","visibility":"public"}`}},
			resource: dns + "/z1",
			missing:  dns + "/nope",
			del:      [2]string{http.MethodDelete, dns + "/z1"},
		},
		{
			name: "pubsub snapshot",
			create: [][3]string{
				{http.MethodPut, ps + "/topics/tp1", `{}`},
				{http.MethodPut, ps + "/subscriptions/sb1", `{"topic":"projects/demo/topics/tp1"}`},
				{http.MethodPut, ps + "/snapshots/sn1", `{"subscription":"projects/demo/subscriptions/sb1"}`},
			},
			resource: ps + "/snapshots/sn1",
			missing:  ps + "/snapshots/nope",
			del:      [2]string{http.MethodDelete, ps + "/snapshots/sn1"},
		},
	}
}

type w5bPolicy struct {
	Version  int    `json:"version"`
	Etag     string `json:"etag"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Bindings []struct {
		Role    string   `json:"role"`
		Members []string `json:"members"`
	} `json:"bindings"`
}

func decodePolicy(t *testing.T, label, body string) w5bPolicy {
	t.Helper()

	var p w5bPolicy
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("%s: decode %q: %v", label, body, err)
	}

	return p
}

// TestW5BResourceIAMVerbs: BigQuery, Spanner, Cloud DNS and Pub/Sub snapshot
// resources serve getIamPolicy/setIamPolicy/testIamPermissions with the IAM
// etag contract, 404 for a missing resource, and a fresh policy after
// delete and recreate.
func TestW5BResourceIAMVerbs(t *testing.T) {
	for _, tc := range w5bCases() {
		t.Run(tc.name, func(t *testing.T) {
			ts := fullServer(t)

			for _, c := range tc.create {
				code, body := do(t, ts, c[0], c[1], c[2])
				wantCode(t, "create "+c[1], code, body, http.StatusOK, "")
			}

			code, body := do(t, ts, http.MethodPost, tc.resource+":getIamPolicy", `{}`)
			wantCode(t, "get unset", code, body, http.StatusOK, "")

			unset := decodePolicy(t, "get unset", body)
			if unset.Version != 1 || unset.Etag == "" || unset.Kind != "" || unset.Name != "" || len(unset.Bindings) != 0 {
				t.Fatalf("unset policy = %s, want version 1, an etag and no resource fields", body)
			}

			set := `{"policy":{"etag":"` + unset.Etag + `","bindings":[{"role":"roles/viewer","members":["user:a@example.com"]}]}}`
			code, body = do(t, ts, http.MethodPost, tc.resource+":setIamPolicy", set)
			wantCode(t, "set", code, body, http.StatusOK, "user:a@example.com")

			if got := decodePolicy(t, "set", body); got.Etag == unset.Etag {
				t.Fatalf("set kept etag %q, want a new one", got.Etag)
			}

			code, body = do(t, ts, http.MethodPost, tc.resource+":setIamPolicy", set)
			wantCode(t, "stale set", code, body, http.StatusConflict, "ABORTED")

			code, body = do(t, ts, http.MethodPost, tc.resource+":getIamPolicy", `{}`)
			wantCode(t, "get after set", code, body, http.StatusOK, "user:a@example.com")

			code, body = do(t, ts, http.MethodPost, tc.resource+":testIamPermissions", `{"permissions":["a.b.c"]}`)
			wantCode(t, "test", code, body, http.StatusOK, "a.b.c")

			code, body = do(t, ts, http.MethodPost, tc.missing+":getIamPolicy", `{}`)
			wantCode(t, "missing", code, body, http.StatusNotFound, "")

			code, body = do(t, ts, tc.del[0], tc.del[1], "")
			if code != http.StatusOK && code != http.StatusNoContent {
				t.Fatalf("delete: code=%d body=%.300s", code, body)
			}

			for _, c := range tc.create {
				code, body = do(t, ts, c[0], c[1], c[2])
				if code != http.StatusOK && code != http.StatusConflict {
					t.Fatalf("recreate %s: code=%d body=%.300s", c[1], code, body)
				}
			}

			code, body = do(t, ts, http.MethodPost, tc.resource+":getIamPolicy", `{}`)
			wantCode(t, "get after recreate", code, body, http.StatusOK, "")

			if strings.Contains(body, "user:a@example.com") {
				t.Fatalf("recreated resource kept the old policy: %s", body)
			}
		})
	}
}

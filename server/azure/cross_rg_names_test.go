package azure_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestGlobalNameInAnotherGroupIsRejected covers resource types whose names are
// global DNS labels. A PUT of a taken name from another resource group fails
// with the real ARM error and leaves the original untouched, while a re-PUT in
// the owning group is still an update.
func TestGlobalNameInAnotherGroupIsRejected(t *testing.T) {
	tests := []struct {
		name     string
		typ      string
		res      string
		body1    string
		body2    string
		wantCode int
		wantErr  string
		field    string // top-level field body2 would have changed
		want     string
	}{
		{
			name: "service bus namespace", typ: "Microsoft.ServiceBus/namespaces", res: "sbdup",
			body1:    `{"location":"westus","sku":{"name":"Standard"}}`,
			body2:    `{"location":"japaneast","sku":{"name":"Premium"}}`,
			wantCode: http.StatusConflict, wantErr: "Conflict", field: "location", want: "westus",
		},
		{
			name: "event hubs namespace", typ: "Microsoft.EventHub/namespaces", res: "ehdup",
			body1:    `{"location":"westus","sku":{"name":"Standard"}}`,
			body2:    `{"location":"japaneast","sku":{"name":"Standard"}}`,
			wantCode: http.StatusConflict, wantErr: "Conflict", field: "location", want: "westus",
		},
		{
			name: "cosmos account", typ: "Microsoft.DocumentDB/databaseAccounts", res: "cosdup",
			body1: `{"location":"westus","properties":{"databaseAccountOfferType":"Standard",` +
				`"locations":[{"locationName":"westus"}]}}`,
			body2: `{"location":"japaneast","properties":{"databaseAccountOfferType":"Standard",` +
				`"locations":[{"locationName":"japaneast"}]}}`,
			wantCode: http.StatusBadRequest, wantErr: "BadRequest", field: "location", want: "westus",
		},
		{
			name: "web site", typ: "Microsoft.Web/sites", res: "sitedup",
			body1:    `{"location":"westus","kind":"app","properties":{}}`,
			body2:    `{"location":"japaneast","kind":"app","properties":{}}`,
			wantCode: http.StatusConflict, wantErr: "Conflict", field: "location", want: "westus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCasClient(t)
			c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)
			c.mustPut(rgPath("rg2"), `{"location":"eastus"}`)

			own := resPath("rg1", tc.typ, tc.res)
			c.mustPut(own, tc.body1)

			code, out := c.do(http.MethodPut, resPath("rg2", tc.typ, tc.res), tc.body2)
			if code != tc.wantCode {
				t.Fatalf("PUT in rg2 = %d %v, want %d", code, out, tc.wantCode)
			}

			if got := errorCode(out); got != tc.wantErr {
				t.Errorf("error code = %q, want %q", got, tc.wantErr)
			}

			code, out = c.do(http.MethodGet, own, "")
			if code != http.StatusOK {
				t.Fatalf("GET rg1 after rejected PUT = %d", code)
			}

			if got, _ := out[tc.field].(string); !strings.EqualFold(got, tc.want) {
				t.Errorf("rg1 %s = %q, want %q (rewritten by the rg2 PUT)", tc.field, got, tc.want)
			}

			if id, _ := out["id"].(string); !strings.Contains(strings.ToLower(id), "/resourcegroups/rg1/") {
				t.Errorf("rg1 id = %q", id)
			}

			c.wantStatus(http.MethodGet, resPath("rg2", tc.typ, tc.res), http.StatusNotFound)

			// The owning group can still update in place.
			if code, out := c.do(http.MethodPut, own, tc.body1); code != http.StatusOK && code != http.StatusCreated {
				t.Fatalf("re-PUT in rg1 = %d %v", code, out)
			}
		})
	}
}

// TestGroupScopedNamesAreIndependent covers resource types whose names are
// unique only within a resource group: the same name in two groups is two
// resources, each read, listed and deleted on its own.
func TestGroupScopedNamesAreIndependent(t *testing.T) {
	tests := []struct {
		name  string
		typ   string
		body1 string
		body2 string
		field func(map[string]any) string
		want1 string
		want2 string
	}{
		{
			name: "log analytics workspace", typ: "Microsoft.OperationalInsights/workspaces",
			body1: `{"location":"westus","properties":{"retentionInDays":30}}`,
			body2: `{"location":"japaneast","properties":{"retentionInDays":60}}`,
			field: func(m map[string]any) string { s, _ := m["location"].(string); return s },
			want1: "westus", want2: "japaneast",
		},
		{
			name: "ssh public key", typ: "Microsoft.Compute/sshPublicKeys",
			body1: `{"location":"eastus","properties":{"publicKey":"ssh-rsa AAAAone one@example"}}`,
			body2: `{"location":"eastus","properties":{"publicKey":"ssh-rsa AAAAtwo two@example"}}`,
			field: func(m map[string]any) string {
				p, _ := m["properties"].(map[string]any)
				s, _ := p["publicKey"].(string)

				return s
			},
			want1: "ssh-rsa AAAAone one@example", want2: "ssh-rsa AAAAtwo two@example",
		},
		{
			name: "vm scale set", typ: "Microsoft.Compute/virtualMachineScaleSets",
			body1: `{"location":"westus","sku":{"name":"Standard_B1s","capacity":1},"properties":{}}`,
			body2: `{"location":"japaneast","sku":{"name":"Standard_B2s","capacity":2},"properties":{}}`,
			field: func(m map[string]any) string { s, _ := m["location"].(string); return s },
			want1: "westus", want2: "japaneast",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCasClient(t)
			c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)
			c.mustPut(rgPath("rg2"), `{"location":"eastus"}`)

			p1 := resPath("rg1", tc.typ, "dup1")
			p2 := resPath("rg2", tc.typ, "dup1")
			c.mustPut(p1, tc.body1)
			c.mustPut(p2, tc.body2)

			for _, chk := range []struct{ path, rg, want string }{{p1, "rg1", tc.want1}, {p2, "rg2", tc.want2}} {
				code, out := c.do(http.MethodGet, chk.path, "")
				if code != http.StatusOK {
					t.Fatalf("GET %s = %d %v", chk.rg, code, out)
				}

				if got := tc.field(out); got != chk.want {
					t.Errorf("%s value = %q, want %q", chk.rg, got, chk.want)
				}

				if id, _ := out["id"].(string); !strings.Contains(strings.ToLower(id), "/resourcegroups/"+chk.rg+"/") {
					t.Errorf("%s id = %q", chk.rg, id)
				}

				if n := len(listValue(c, rgPath(chk.rg)+"/providers/"+tc.typ)); n != 1 {
					t.Errorf("%s list has %d items, want 1", chk.rg, n)
				}
			}

			// A subscription-wide list carries each item's own group in its id.
			ids := map[string]bool{}
			for _, it := range listValue(c, "/subscriptions/"+casSub+"/providers/"+tc.typ) {
				id, _ := it["id"].(string)
				ids[strings.ToLower(id)] = true
			}

			for _, rg := range []string{"rg1", "rg2"} {
				id := strings.ToLower(resPath(rg, tc.typ, "dup1"))
				if !ids[id] {
					t.Errorf("subscription list misses %s (got %v)", id, ids)
				}
			}

			if code, out := c.do(http.MethodDelete, p1, ""); code < 200 || code > 299 {
				t.Fatalf("DELETE rg1 = %d %v", code, out)
			}

			c.wantStatus(http.MethodGet, p1, http.StatusNotFound)

			code, out := c.do(http.MethodGet, p2, "")
			if code != http.StatusOK || tc.field(out) != tc.want2 {
				t.Errorf("rg2 after rg1 delete = %d %v", code, out)
			}
		})
	}
}

func errorCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	code, _ := e["code"].(string)

	return code
}

func listValue(c *casClient, path string) []map[string]any {
	c.t.Helper()

	code, out := c.do(http.MethodGet, path, "")
	if code != http.StatusOK {
		c.t.Fatalf("GET %s = %d %v", path, code, out)
	}

	raw, _ := out["value"].([]any)
	items := make([]map[string]any, 0, len(raw))

	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			items = append(items, m)
		}
	}

	return items
}

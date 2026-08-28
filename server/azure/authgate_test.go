package azure

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/authctx"
)

// jwtWith builds an unsigned three-part JWT carrying the given claims, the shape
// the gate parses (it validates claims, not the signature).
func jwtWith(t *testing.T, claims map[string]any) string {
	t.Helper()

	enc := base64.RawURLEncoding.EncodeToString

	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	return enc(header) + "." + enc(payload) + "." + enc([]byte("sig"))
}

// runGate feeds a request carrying authHeader through the gate under fixedNow and
// returns whether dispatch proceeded plus the recorded response.
func runGate(t *testing.T, authHeader string, fixedNow time.Time) (bool, *authctx.AzurePrincipal, *httptest.ResponseRecorder) {
	t.Helper()

	gate := newAuthGate(config.NewFakeClock(fixedNow))

	req := httptest.NewRequest(http.MethodGet, "/subscriptions/sub/resource", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	rec := httptest.NewRecorder()
	out, proceed := gate(rec, req)

	var principal *authctx.AzurePrincipal
	if p, ok := authctx.AzurePrincipalFrom(out.Context()); ok {
		principal = &p
	}

	return proceed, principal, rec
}

func assertGate401(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate header")
	}

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}

	if envelope.Error.Code != invalidTokenCode {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, invalidTokenCode)
	}
}

func TestAuthGateRejections(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		authHeader string
		claims     map[string]any // when set, wrapped as "Bearer <jwt>"
	}{
		{name: "MissingHeader"},
		{name: "NotBearer", authHeader: "Basic abc"},
		{name: "EmptyBearer", authHeader: "Bearer "},
		{name: "Malformed", authHeader: "Bearer aaa.bbb"},
		{name: "WrongAudience", claims: map[string]any{"aud": "https://evil.example.com", "oid": "o1"}},
		{name: "EmptyAudience", claims: map[string]any{"aud": "", "oid": "o1"}},
		{name: "NoPrincipal", claims: map[string]any{"aud": "https://management.azure.com"}},
		{
			name:   "Expired",
			claims: map[string]any{"aud": "https://management.azure.com", "oid": "o1", "exp": float64(now.Add(-time.Minute).Unix())},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := tc.authHeader
			if tc.claims != nil {
				header = "Bearer " + jwtWith(t, tc.claims)
			}

			proceed, _, rec := runGate(t, header, now)
			if proceed {
				t.Fatal("gate proceeded, want rejection")
			}

			assertGate401(t, rec)
		})
	}
}

func TestAuthGateResolvesPrincipal(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		claims     map[string]any
		wantObject string
		wantApp    string
		wantTenant string
	}{
		{
			name:       "PrefersOid",
			claims:     map[string]any{"aud": "https://management.azure.com", "oid": "obj-1", "appid": "app-1", "tid": "tenant-1"},
			wantObject: "obj-1", wantApp: "app-1", wantTenant: "tenant-1",
		},
		{
			name:    "AppIDWhenNoOid",
			claims:  map[string]any{"aud": "https://vault.azure.net", "appid": "app-2"},
			wantApp: "app-2",
		},
		{
			name:    "AzpWhenNoAppid",
			claims:  map[string]any{"aud": "https://storage.azure.com", "azp": "app-3"},
			wantApp: "app-3",
		},
		{
			name:       "SubWhenNoOidOrApp",
			claims:     map[string]any{"aud": "https://management.core.windows.net/", "sub": "subj-1"},
			wantObject: "subj-1",
		},
		{
			name:       "AudienceArrayAccepted",
			claims:     map[string]any{"aud": []string{"https://evil.example.com", "https://management.azure.com"}, "oid": "obj-4"},
			wantObject: "obj-4",
		},
		{
			name:       "AbsentExpAccepted",
			claims:     map[string]any{"aud": "https://management.azure.com", "oid": "obj-5"},
			wantObject: "obj-5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proceed, principal, _ := runGate(t, "Bearer "+jwtWith(t, tc.claims), now)
			if !proceed {
				t.Fatal("gate rejected a valid token, want proceed")
			}

			if principal == nil {
				t.Fatal("no principal attached to context")
			}

			if principal.ObjectID != tc.wantObject || principal.AppID != tc.wantApp || principal.TenantID != tc.wantTenant {
				t.Fatalf("principal = %+v, want object=%q app=%q tenant=%q",
					*principal, tc.wantObject, tc.wantApp, tc.wantTenant)
			}
		})
	}
}

// TestAuthGateDefaultOff confirms a server built without EnforceAuth installs no
// gate: an unauthenticated request is not rejected at the pre-dispatch stage
// (it reaches handler matching, here yielding the dispatcher's 501 rather than a
// 401 auth error).
func TestAuthGateDefaultOff(t *testing.T) {
	srv := New(Drivers{})

	req := httptest.NewRequest(http.MethodGet, "/subscriptions/sub/resource?api-version=2022-12-01", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("default-off server rejected an unauthenticated request with 401")
	}
}

// TestAuthGateEnabledRejectsUnauthenticated confirms a server built with
// EnforceAuth installs the gate: an unauthenticated request is rejected with a
// 401 before any handler runs.
func TestAuthGateEnabledRejectsUnauthenticated(t *testing.T) {
	srv := New(Drivers{EnforceAuth: true})

	req := httptest.NewRequest(http.MethodGet, "/subscriptions/sub/resource?api-version=2022-12-01", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 under EnforceAuth", rec.Code)
	}

	body, _ := io.ReadAll(rec.Body)

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error body %q: %v", string(body), err)
	}

	if envelope.Error.Code != invalidTokenCode {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, invalidTokenCode)
	}
}

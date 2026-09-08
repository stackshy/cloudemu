package binaryauthorization_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"github.com/stackshy/cloudemu/v2/server/gcp/binaryauthorization"
)

func TestMatchesClaimsOnlyPolicyAndAttestors(t *testing.T) {
	h := binaryauthorization.New(cloudemu.NewGCP().BinaryAuthorization)

	cases := []struct {
		path string
		want bool
	}{
		{"/v1/projects/p/policy", true},
		{"/v1/projects/p/attestors", true},
		{"/v1/projects/p/attestors/a1", true},
		{"/v1/projects/p/attestors/a1:getIamPolicy", true},
		{"/v1/projects/p/attestors/a1:setIamPolicy", true},
		// Never claims operations or any sibling grammar.
		{"/v1/projects/p/operations/op-1", false},
		{"/v1/projects/p/notes/n1", false},
		{"/v1/projects/p/policy/extra", false},
		{"/v1/projects/p", false},
		{"/v1/projects/p/attestors/a1/foo", false},
		// System policy (a different top-level grammar) is not claimed.
		{"/v1/locations/us/policy", false},
		{"/v2/projects/p/policy", false},
	}

	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if got := h.Matches(r); got != c.want {
			t.Errorf("Matches(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestIntEnumTolerance drives updatePolicy with the enum fields encoded as their
// protojson integers (GAPIC clients marshal enums as numbers) and confirms the
// stored + echoed values are the canonical string names — including enums nested
// inside the opaque defaultAdmissionRule block.
func TestIntEnumTolerance(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{BinaryAuthorization: cloud.BinaryAuthorization})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// globalPolicyEvaluationMode 2 == DISABLE; evaluationMode 2 ==
	// REQUIRE_ATTESTATION; enforcementMode 1 == ENFORCED_BLOCK_AND_AUDIT_LOG.
	body := map[string]any{
		"globalPolicyEvaluationMode": 2,
		"defaultAdmissionRule": map[string]any{
			"evaluationMode":  2,
			"enforcementMode": 1,
		},
	}

	buf, _ := json.Marshal(body)
	url := ts.URL + "/v1/projects/p/policy"

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got struct {
		GlobalPolicyEvaluationMode string `json:"globalPolicyEvaluationMode"`
		DefaultAdmissionRule       struct {
			EvaluationMode  string `json:"evaluationMode"`
			EnforcementMode string `json:"enforcementMode"`
		} `json:"defaultAdmissionRule"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.GlobalPolicyEvaluationMode != "DISABLE" {
		t.Fatalf("globalPolicyEvaluationMode = %q, want DISABLE", got.GlobalPolicyEvaluationMode)
	}

	if got.DefaultAdmissionRule.EvaluationMode != "REQUIRE_ATTESTATION" {
		t.Fatalf("nested evaluationMode = %q, want REQUIRE_ATTESTATION", got.DefaultAdmissionRule.EvaluationMode)
	}

	if got.DefaultAdmissionRule.EnforcementMode != "ENFORCED_BLOCK_AND_AUDIT_LOG" {
		t.Fatalf("nested enforcementMode = %q, want ENFORCED_BLOCK_AND_AUDIT_LOG", got.DefaultAdmissionRule.EnforcementMode)
	}
}

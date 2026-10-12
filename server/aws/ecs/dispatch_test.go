package ecs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// bareECS satisfies driver.ECS without any of the optional capabilities.
type bareECS struct{ driver.ECS }

func serve(t *testing.T, h *Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Amz-Target", targetPrefix+target)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestHandler_NewOpsRoutedAndGated(t *testing.T) {
	h := New(bareECS{})

	newOps := []string{
		"CreateTaskSet", "UpdateTaskSet", "DeleteTaskSet", "DescribeTaskSets", "UpdateServicePrimaryTaskSet",
		"GetTaskProtection", "UpdateTaskProtection",
		"ListServiceDeployments", "DescribeServiceDeployments", "DescribeServiceRevisions", "StopServiceDeployment",
		"ListServicesByNamespace",
	}

	for _, op := range newOps {
		rec := serve(t, h, op, `{}`)

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UnsupportedFeatureException") {
			t.Fatalf("%s on a backend without the capability: status %d body %s", op, rec.Code, rec.Body.String())
		}
	}

	rec := serve(t, h, "NoSuchOperation", `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ClientException") {
		t.Fatalf("unknown op must stay a ClientException: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_MatchesIsPrefixGated(t *testing.T) {
	h := New(bareECS{})

	for header, want := range map[string]bool{
		targetPrefix + "ListClusters":              true,
		targetPrefix + "ListServicesByNamespace":   true,
		"AmazonEC2ContainerServiceV2.ListClusters": false,
		"DynamoDB_20120810.ListTables":             false,
		"":                                         false,
	} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Amz-Target", header)

		if got := h.Matches(req); got != want {
			t.Fatalf("Matches(%q) = %v, want %v", header, got, want)
		}
	}
}

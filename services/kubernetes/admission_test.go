// Admission webhook tests: a validating webhook served over TLS by
// httptest.NewTLSServer denies a Pod create when admission is enabled, and is
// never invoked at all when it's disabled (the default) — the feature must
// not change existing behavior unless explicitly turned on.

package kubernetes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	admissionregv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const admissionDenyMessage = "pods are not allowed in this test cluster"

// newDenyingWebhook returns a TLS test server that answers every
// AdmissionReview with allowed=false and admissionDenyMessage, and a counter
// of how many times it was called.
func newDenyingWebhook(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()

	var calls int32

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)

		var review admissionv1.AdmissionReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			t.Errorf("webhook: decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&admissionv1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
			Response: &admissionv1.AdmissionResponse{
				UID:     review.Request.UID,
				Allowed: false,
				Result:  &metav1.Status{Message: admissionDenyMessage, Code: http.StatusForbidden, Reason: metav1.StatusReasonForbidden},
			},
		})
	}))
	t.Cleanup(ts.Close)

	return ts, &calls
}

// registerDenyingValidatingWebhook stores a ValidatingWebhookConfiguration
// whose single webhook rule matches CREATE on core/v1 pods and points at
// webhookURL.
func registerDenyingValidatingWebhook(t *testing.T, base, webhookURL string) {
	t.Helper()

	fail := admissionregv1.Fail
	sideEffects := admissionregv1.SideEffectClassNone

	cfg := &admissionregv1.ValidatingWebhookConfiguration{
		TypeMeta:   metav1.TypeMeta{Kind: "ValidatingWebhookConfiguration", APIVersion: "admissionregistration.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "deny-pods"},
		Webhooks: []admissionregv1.ValidatingWebhook{{
			Name:         "deny.pods.cloudemu.test",
			ClientConfig: admissionregv1.WebhookClientConfig{URL: &webhookURL},
			Rules: []admissionregv1.RuleWithOperations{{
				Operations: []admissionregv1.OperationType{admissionregv1.Create},
				Rule: admissionregv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
				},
			}},
			FailurePolicy:           &fail,
			SideEffects:             &sideEffects,
			AdmissionReviewVersions: []string{"v1"},
		}},
	}

	resp := do(t, http.MethodPost, base+"/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations", mustJSON(t, cfg))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register validating webhook config: got %d, want 201", resp.StatusCode)
	}
}

func testPodBody(t *testing.T, name string) []byte {
	t.Helper()

	return mustJSON(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "nginx"}}},
	})
}

func TestAdmission_ValidatingWebhookDeniesCreate(t *testing.T) {
	webhook, calls := newDenyingWebhook(t)

	api := kubernetes.NewAPIServer()
	api.SetAdmissionEnabled(true)
	api.SetAdmissionHTTPClient(webhook.Client())

	uid, _ := api.RegisterCluster()

	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	base := server.URL + "/k8s/" + uid

	registerDenyingValidatingWebhook(t, base, webhook.URL)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", testPodBody(t, "blocked"))
	if resp.StatusCode < http.StatusBadRequest {
		t.Fatalf("create status: got %d, want 4xx (denied)", resp.StatusCode)
	}

	var status metav1.Status
	mustDecode(t, resp.Body, &status)

	if status.Message != admissionDenyMessage {
		t.Fatalf("denial message: got %q, want %q", status.Message, admissionDenyMessage)
	}

	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("webhook calls: got %d, want 1", got)
	}

	// The Pod must not have been persisted.
	resp2 := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/blocked", nil)
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("get after denied create: got %d, want 404", resp2.StatusCode)
	}
}

func TestAdmission_DisabledByDefaultSkipsWebhook(t *testing.T) {
	webhook, calls := newDenyingWebhook(t)

	// Admission is left disabled (the default) — SetAdmissionEnabled is never
	// called, so the webhook config below is stored but never invoked.
	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterCluster()

	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	base := server.URL + "/k8s/" + uid

	registerDenyingValidatingWebhook(t, base, webhook.URL)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", testPodBody(t, "allowed"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status: got %d, want 201 (admission disabled, webhook must not run)", resp.StatusCode)
	}

	if got := atomic.LoadInt32(calls); got != 0 {
		t.Fatalf("webhook calls: got %d, want 0 (admission disabled)", got)
	}
}

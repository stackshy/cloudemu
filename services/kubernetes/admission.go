package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// Operation values sent on the AdmissionRequest — the two this server ever
// issues a write for (registryDelete has no admission call site: deletes
// aren't in scope for this pass).
const (
	opCreate = "CREATE"
	opUpdate = "UPDATE"
)

const (
	admissionReviewAPIVersion = "admission.k8s.io/v1"
	admissionReviewKind       = "AdmissionReview"

	// defaultAdmissionTimeout bounds both the outbound HTTP call to a webhook
	// and the ClusterState's default *http.Client when none was injected via
	// APIServer.SetAdmissionHTTPClient.
	defaultAdmissionTimeout = 5 * time.Second

	// admissionDeniedStatusCode is the HTTP status used for a denial whose
	// AdmissionResponse.status.code was left unset — matching real apiserver,
	// which defaults a webhook denial to 403 Forbidden.
	admissionDeniedStatusCode = http.StatusForbidden
)

// gvr returns the GroupVersionResource a registry-backed kind is served
// under — what a webhook's rules[] and an AdmissionRequest match against.
func (d *resourceDef) gvr() metav1.GroupVersionResource {
	return metav1.GroupVersionResource{Group: d.group, Version: d.version, Resource: d.plural}
}

// gvrPods and gvrDeployments are the GVRs for the two typed (non-registry)
// write paths that run admission — core/v1 Pods and apps/v1 Deployments.
func gvrPods() metav1.GroupVersionResource {
	return metav1.GroupVersionResource{Version: apiVersionV1, Resource: "pods"}
}

func gvrDeployments() metav1.GroupVersionResource {
	return metav1.GroupVersionResource{Group: apiGroupApps, Version: apiVersionV1, Resource: resourceDeployments}
}

// webhookCall is the subset of a Mutating/ValidatingWebhook this server acts
// on — the two webhook kinds are structurally identical here bar their Go
// type, so both extraction paths normalize into this.
type webhookCall struct {
	name          string
	url           string
	failurePolicy admissionregv1.FailurePolicyType
	rules         []admissionregv1.RuleWithOperations
}

// matches reports whether op+gvr falls under any of the webhook's rules.
func (c webhookCall) matches(op string, gvr metav1.GroupVersionResource) bool {
	for i := range c.rules {
		if admissionRuleMatches(&c.rules[i], op, gvr) {
			return true
		}
	}

	return false
}

func admissionRuleMatches(rule *admissionregv1.RuleWithOperations, op string, gvr metav1.GroupVersionResource) bool {
	return operationMatches(rule.Operations, op) &&
		stringMatches(rule.APIGroups, gvr.Group) &&
		stringMatches(rule.APIVersions, gvr.Version) &&
		stringMatches(rule.Resources, gvr.Resource)
}

const admissionWildcard = "*"

func operationMatches(ops []admissionregv1.OperationType, op string) bool {
	for _, o := range ops {
		if o == admissionregv1.OperationAll || string(o) == op {
			return true
		}
	}

	return false
}

func stringMatches(values []string, want string) bool {
	for _, v := range values {
		if v == admissionWildcard || v == want {
			return true
		}
	}

	return false
}

func failurePolicyOrDefault(p *admissionregv1.FailurePolicyType) admissionregv1.FailurePolicyType {
	if p == nil {
		// Real apiserver defaults an unset failurePolicy to Fail.
		return admissionregv1.Fail
	}

	return *p
}

func webhookURL(cc admissionregv1.WebhookClientConfig) string {
	if cc.URL == nil {
		return ""
	}

	return *cc.URL
}

// rawWebhook is the JSON shape shared by admissionregistration/v1's
// MutatingWebhook and ValidatingWebhook — decoding into this one local type
// (rather than the full typed {Mutating,Validating}WebhookConfiguration)
// lets webhookCallsFromConfig serve both kinds without duplicating the
// extraction logic.
type rawWebhook struct {
	Name          string                              `json:"name"`
	ClientConfig  admissionregv1.WebhookClientConfig  `json:"clientConfig"`
	Rules         []admissionregv1.RuleWithOperations `json:"rules"`
	FailurePolicy *admissionregv1.FailurePolicyType   `json:"failurePolicy"`
}

// webhookCallsFromConfig decodes the webhooks[] of one stored
// {Mutating,Validating}WebhookConfiguration into webhookCalls this server can
// invoke. Service-ref clientConfig is not supported — only a direct
// clientConfig.url, per this phase's scope.
func webhookCallsFromConfig(cfg *unstructured.Unstructured) []webhookCall {
	items, found, err := unstructured.NestedSlice(cfg.Object, "webhooks")
	if err != nil || !found {
		return nil
	}

	out := make([]webhookCall, 0, len(items))

	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		var raw rawWebhook
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &raw); err != nil {
			continue
		}

		out = append(out, webhookCall{
			name: raw.Name, url: webhookURL(raw.ClientConfig),
			failurePolicy: failurePolicyOrDefault(raw.FailurePolicy), rules: raw.Rules,
		})
	}

	return out
}

// matchingWebhooksLocked reads the registry store for plural (one of the two
// webhook config kinds) and returns every webhookCall whose rules match
// op+gvr, in stored-key order. Callers hold s.mu.
func (s *ClusterState) matchingWebhooksLocked(plural, op string, gvr metav1.GroupVersionResource) []webhookCall {
	store := s.reg.stores[regKey(apiGroupAdmissionRegistration, apiVersionV1, plural)]
	if store == nil {
		return nil
	}

	keys := make([]string, 0, len(store.items))
	for k := range store.items {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var out []webhookCall

	for _, k := range keys {
		for _, wh := range webhookCallsFromConfig(store.items[k]) {
			if wh.matches(op, gvr) {
				out = append(out, wh)
			}
		}
	}

	return out
}

// runAdmission runs every matching mutating webhook (applying its patch, if
// any, before the next one sees the object) and then every matching
// validating webhook against the final object. Callers hold s.mu — the
// outbound HTTP call therefore runs with the cluster lock held, which is an
// accepted simplification for an opt-in, deliberately non-concurrent mock.
//
// Returns the mutated object (nil if no mutating webhook patched it) and, on
// a denial or an unrecoverable failurePolicy=Fail error, the Status to
// return to the client instead of persisting the write.
func (s *ClusterState) runAdmission(
	op string, gvr metav1.GroupVersionResource, obj *unstructured.Unstructured,
) (*unstructured.Unstructured, *metav1.Status) {
	current := obj
	mutated := false

	for _, wh := range s.matchingWebhooksLocked(pluralMutatingWebhooks, op, gvr) {
		resp, denied := s.callWebhook(wh, op, gvr, current)
		if denied != nil {
			return nil, denied
		}

		if patched, ok := applyAdmissionPatch(current, resp); ok {
			current = patched
			mutated = true
		}
	}

	for _, wh := range s.matchingWebhooksLocked(pluralValidatingWebhooks, op, gvr) {
		if _, denied := s.callWebhook(wh, op, gvr, current); denied != nil {
			return nil, denied
		}
	}

	if mutated {
		return current, nil
	}

	return nil, nil
}

// callWebhook invokes one webhook and folds its failurePolicy into the
// result: a transport/decode failure under Ignore is swallowed (resp=nil,
// denied=nil); under Fail (the default) it becomes a denial. An explicit
// allowed=false response always becomes a denial regardless of failurePolicy.
func (s *ClusterState) callWebhook(
	wh webhookCall, op string, gvr metav1.GroupVersionResource, obj *unstructured.Unstructured,
) (*admissionv1.AdmissionResponse, *metav1.Status) {
	resp, ok := s.postAdmissionReview(wh, op, gvr, obj)
	if !ok {
		if wh.failurePolicy == admissionregv1.Ignore {
			return nil, nil
		}

		return nil, &metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Code:     http.StatusInternalServerError,
			Reason:   metav1.StatusReasonInternalError,
			Message:  "k8s api: admission webhook " + wh.name + " call failed",
		}
	}

	if !resp.Allowed {
		return resp, deniedStatus(wh.name, resp.Result)
	}

	return resp, nil
}

// deniedStatus builds the Status returned to the client for a webhook
// denial, filling in the parts a webhook's AdmissionResponse.status left
// unset (real apiserver does the same).
func deniedStatus(name string, result *metav1.Status) *metav1.Status {
	out := &metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure}
	if result != nil {
		out = result.DeepCopy()
	}

	if out.Code == 0 {
		out.Code = admissionDeniedStatusCode
	}

	if out.Reason == "" {
		out.Reason = metav1.StatusReasonForbidden
	}

	if out.Message == "" {
		out.Message = "k8s api: admission webhook " + name + " denied the request"
	}

	return out
}

// applyAdmissionPatch applies a mutating webhook's JSONPatch (RFC 6902)
// response to cur, returning ok=false if the response carried no patch or
// the patch didn't apply cleanly (treated as a no-op mutation rather than a
// hard failure — only allowed/denied is contractual).
func applyAdmissionPatch(cur *unstructured.Unstructured, resp *admissionv1.AdmissionResponse) (*unstructured.Unstructured, bool) {
	if resp == nil || len(resp.Patch) == 0 || resp.PatchType == nil || *resp.PatchType != admissionv1.PatchTypeJSONPatch {
		return nil, false
	}

	curBytes, err := json.Marshal(cur.Object)
	if err != nil {
		return nil, false
	}

	p, err := jsonpatch.DecodePatch(resp.Patch)
	if err != nil {
		return nil, false
	}

	merged, err := p.Apply(curBytes)
	if err != nil {
		return nil, false
	}

	out := &unstructured.Unstructured{}
	if err := out.UnmarshalJSON(merged); err != nil {
		return nil, false
	}

	return out, true
}

// postAdmissionReview POSTs an AdmissionReview request for obj to wh.url and
// decodes the response. ok=false covers every way the call didn't produce a
// usable AdmissionResponse (no URL configured, transport error, bad JSON) —
// the caller applies failurePolicy to decide what that means.
func (s *ClusterState) postAdmissionReview(
	wh webhookCall, op string, gvr metav1.GroupVersionResource, obj *unstructured.Unstructured,
) (*admissionv1.AdmissionResponse, bool) {
	if wh.url == "" {
		return nil, false
	}

	body, err := json.Marshal(buildAdmissionReview(op, gvr, obj))
	if err != nil {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultAdmissionTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.url, bytes.NewReader(body))
	if err != nil {
		return nil, false
	}

	req.Header.Set("Content-Type", contentTypeJSON)

	httpResp, err := s.admissionClient.Do(req)
	if err != nil {
		return nil, false
	}

	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, false
	}

	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(respBody, &review); err != nil || review.Response == nil {
		return nil, false
	}

	return review.Response, true
}

func buildAdmissionReview(op string, gvr metav1.GroupVersionResource, obj *unstructured.Unstructured) *admissionv1.AdmissionReview {
	raw, _ := json.Marshal(obj.Object)

	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: admissionReviewAPIVersion, Kind: admissionReviewKind},
		Request: &admissionv1.AdmissionRequest{
			UID:       types.UID(newUID()),
			Kind:      metav1.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: obj.GetKind()},
			Resource:  gvr,
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
			Operation: admissionv1.Operation(op),
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

// admit is the call-site entry point: it runs the admission chain (a no-op
// when admission is disabled, the default) and, on a denial, writes the
// Status response itself and reports handled=true so the caller aborts the
// write without persisting anything. obj is any pointer accepted by
// runtime.DefaultUnstructuredConverter (a typed *corev1.Pod/*appsv1.Deployment,
// or a *unstructured.Unstructured for the registry path) and is mutated in
// place when a mutating webhook returned a patch. Callers hold s.mu.
func (s *ClusterState) admit(w http.ResponseWriter, op string, gvr metav1.GroupVersionResource, obj any) bool {
	if !s.admissionEnabled {
		return false
	}

	u, ok := toAdmissionUnstructured(obj)
	if !ok {
		return false
	}

	mutated, denied := s.runAdmission(op, gvr, u)
	if denied != nil {
		writeStatus(w, int(denied.Code), denied.Reason, denied.Message)

		return true
	}

	if mutated != nil {
		applyAdmissionResult(obj, mutated)
	}

	return false
}

func toAdmissionUnstructured(obj any) (*unstructured.Unstructured, bool) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u, true
	}

	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, false
	}

	return &unstructured.Unstructured{Object: m}, true
}

func applyAdmissionResult(obj any, mutated *unstructured.Unstructured) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		*u = *mutated

		return
	}

	_ = runtime.DefaultUnstructuredConverter.FromUnstructured(mutated.Object, obj)
}

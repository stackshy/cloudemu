package kubernetes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

// kubectl and client-go send built-in kinds as protobuf on writes. The server
// must decode a protobuf-framed request body (not 415 it) or `kubectl create`,
// `apply`, `scale`, etc. cannot write anything. These tests drive the exact
// wire encoding kubectl uses against both a typed handler (ConfigMap) and a
// registry handler (ReplicaSet, decoded to unstructured).

func protobufEncode(t *testing.T, obj runtime.Object, gv runtime.GroupVersioner) []byte {
	t.Helper()

	info, ok := runtime.SerializerInfoForMediaType(clientgoscheme.Codecs.SupportedMediaTypes(),
		runtime.ContentTypeProtobuf)
	if !ok {
		t.Fatal("no protobuf serializer registered")
	}

	body, err := runtime.Encode(clientgoscheme.Codecs.EncoderForVersion(info.Serializer, gv), obj)
	if err != nil {
		t.Fatalf("protobuf encode: %v", err)
	}

	return body
}

func postProtobuf(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", runtime.ContentTypeProtobuf)
	req.Header.Set("Accept", runtime.ContentTypeProtobuf+",application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	return resp
}

func TestProtobufWrite_TypedConfigMap(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	ns := do(t, http.MethodPost, base+"/api/v1/namespaces",
		[]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"default"}}`))
	ns.Body.Close()

	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "settings"},
		Data:       map[string]string{"color": "blue"},
	}
	body := protobufEncode(t, cm, corev1.SchemeGroupVersion)

	resp := postProtobuf(t, base+"/api/v1/namespaces/default/configmaps", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("protobuf create configmap: status %d, want 201", resp.StatusCode)
	}

	// Read it back (JSON) and confirm the payload decoded correctly.
	got := do(t, http.MethodGet, base+"/api/v1/namespaces/default/configmaps/settings", nil)
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get configmap: status %d", got.StatusCode)
	}
}

func TestProtobufWrite_RegistryReplicaSet(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	ns := do(t, http.MethodPost, base+"/api/v1/namespaces",
		[]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"default"}}`))
	ns.Body.Close()

	replicas := int32(2)
	rs := &appsv1.ReplicaSet{
		TypeMeta:   metav1.TypeMeta{Kind: "ReplicaSet", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "nginx"}}},
			},
		},
	}
	body := protobufEncode(t, rs, appsv1.SchemeGroupVersion)

	resp := postProtobuf(t, base+"/apis/apps/v1/namespaces/default/replicasets", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("protobuf create replicaset: status %d, want 201", resp.StatusCode)
	}

	// The registry decodes protobuf → typed → unstructured; replicas must
	// survive as 2 (an int64, not float64) so the reconciler materializes Pods.
	pods := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods?labelSelector=app%3Dweb", nil)
	defer pods.Body.Close()
	var list corev1.PodList
	if err := json.NewDecoder(pods.Body).Decode(&list); err != nil {
		t.Fatalf("decode pods: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("replicaset materialized %d pods, want 2", len(list.Items))
	}
}

package kubernetes

import (
	"net/http"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

// decodeProtobufBody decodes a Kubernetes protobuf-framed request body into v.
//
// kubectl and client-go send built-in kinds as protobuf on writes. The
// client-go scheme's recognizing deserializer unwraps the runtime.Unknown
// envelope and produces the typed object; the typed handlers decode straight
// into their object, while the registry handlers (which work in unstructured)
// get the decoded object converted. Responses stay JSON — the callers' Accept
// header includes application/json, so no protobuf encoder is needed.
func decodeProtobufBody(w http.ResponseWriter, body []byte, v any) bool {
	obj, gvk, err := clientgoscheme.Codecs.UniversalDeserializer().Decode(body, nil, typedTarget(v))
	if err != nil {
		writeBadRequest(w, "k8s api: decode protobuf body: "+err.Error())

		return false
	}

	dst, ok := v.(*unstructured.Unstructured)
	if !ok {
		// A concrete typed object (e.g. *corev1.Pod) was decoded in place.
		return true
	}

	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		writeBadRequest(w, "k8s api: convert protobuf object: "+err.Error())

		return false
	}

	dst.Object = m
	// ToUnstructured omits apiVersion/kind when the source object's TypeMeta is
	// empty; restore them from the envelope's GVK so the registry stores a fully
	// self-describing object.
	if gvk != nil {
		dst.SetAPIVersion(gvk.GroupVersion().String())
		dst.SetKind(gvk.Kind)
	}

	return true
}

// typedTarget returns v as the in-place decode target when it is a concrete
// typed object, or nil for unstructured — protobuf can't decode into
// unstructured, so we let the deserializer allocate the typed object from the
// scheme and convert it afterwards.
func typedTarget(v any) runtime.Object {
	if _, ok := v.(*unstructured.Unstructured); ok {
		return nil
	}
	if o, ok := v.(runtime.Object); ok {
		return o
	}

	return nil
}

package kubernetes

import (
	"net/http"
	"strings"

	openapiv2 "github.com/google/gnostic-models/openapiv2"
	"google.golang.org/protobuf/proto"
)

// serveOpenAPI answers the OpenAPI discovery endpoints (v2 and v3).
//
// kubectl (>=1.24) validates `apply`/`create` payloads against the server's
// OpenAPI document and PREFERS v3. For each object it looks the GVK up in the
// v3 group document; if it isn't found kubectl falls back to fetching
// /openapi/v2 as *protobuf* — which the emulator does not encode — and
// `kubectl apply` then dies with "failed to download openapi" before ever
// sending the object.
//
// So the v3 group documents list every served kind with a permissive schema
// (type: object, additionalProperties: true) carrying the
// x-kubernetes-group-version-kind extension kubectl matches on. The GVK is
// therefore always found, kubectl stays on the JSON v3 path, and validation
// accepts any well-formed object without the emulator asserting (and risking
// mis-asserting) field-level API shapes.
//
// The documents are identical for every cluster, so this is served
// prefix-independently: both at /k8s/<uid>/openapi/... and at the bare
// /openapi/... path the v3 serverRelativeURL follow-ups use.
//
// Returns false when the path is not an OpenAPI request.
func serveOpenAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	path := strings.TrimSuffix(r.URL.Path, "/")
	// Strip a possible /k8s/<uid> prefix so both the prefixed initial request
	// and the prefix-less serverRelativeURL follow-ups resolve here.
	if strings.HasPrefix(path, pathPrefix) {
		if i := strings.Index(path, "/openapi/"); i >= 0 {
			path = path[i:]
		}
	}

	switch {
	case path == "/openapi/v3":
		writeJSON(w, http.StatusOK, openAPIV3Root())

		return true
	case strings.HasPrefix(path, "/openapi/v3/"):
		gvPath := strings.TrimPrefix(path, "/openapi/v3/")
		writeJSON(w, http.StatusOK, openAPIV3Doc(gvPath))

		return true
	case path == "/openapi/v2":
		serveOpenAPIV2(w, r)

		return true
	}

	return false
}

// serveOpenAPIV2 serves the Swagger 2.0 document. kubectl's legacy validator
// (the v3 fallback) requests it as protobuf and reads the raw bytes, so we
// return a protobuf-encoded document for a protobuf Accept and JSON otherwise.
//
// The Content-Type for the protobuf form is deliberately application/octet-
// stream, NOT the canonical application/com.github.proto-openapi.spec.v2@v1.0
// +protobuf: Go's mime.ParseMediaType (which client-go runs over the response
// header) rejects the '@', failing the whole download. The consumer reads the
// body as raw bytes and proto-unmarshals it, so the octet-stream type is
// harmless and keeps the parser happy.
func serveOpenAPIV2(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "protobuf") {
		doc := &openapiv2.Document{
			Swagger:     "2.0",
			Info:        &openapiv2.Info{Title: "cloudemu-kubernetes", Version: "v1.29.0-cloudemu"},
			Paths:       &openapiv2.Paths{},
			Definitions: &openapiv2.Definitions{},
		}

		b, err := proto.Marshal(doc)
		if err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", "k8s api: encode openapi: "+err.Error())

			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)

		return
	}

	// helm and older tooling probe v2 as JSON; a minimal but valid Swagger 2.0
	// envelope with no definitions means "nothing to contradict".
	writeJSON(w, http.StatusOK, map[string]any{
		"swagger":     "2.0",
		"info":        map[string]any{"title": "cloudemu-kubernetes", "version": "v1.29.0-cloudemu"},
		"paths":       map[string]any{},
		"definitions": map[string]any{},
	})
}

// openAPIV3Root lists a relative document URL per served group-version. kubectl
// fetches the one matching the object it is validating.
func openAPIV3Root() map[string]any {
	paths := map[string]any{
		"api/v1": groupV3Ref("api/v1"),
	}

	for _, gv := range discoveryGroups() {
		p := "apis/" + gv.group + "/" + gv.version
		paths[p] = groupV3Ref(p)
	}

	return map[string]any{"paths": paths}
}

func groupV3Ref(p string) map[string]any {
	// A static hash is fine — the document never changes for the process, and
	// kubectl only uses the hash to cache-bust, not to validate.
	return map[string]any{"serverRelativeURL": "/openapi/v3/" + p + "?hash=cloudemu"}
}

// openAPIV3Doc builds the OpenAPI v3 document for a group-version path
// ("api/v1" or "apis/<group>/<version>"), with a permissive schema per served
// kind so kubectl resolves every GVK it validates.
func openAPIV3Doc(gvPath string) map[string]any {
	group, version := parseGVPath(gvPath)

	schemas := map[string]any{}
	for _, kind := range kindsForGroupVersion(group, version) {
		schemas[schemaKey(group, version, kind)] = map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"x-kubernetes-group-version-kind": []map[string]any{
				{"group": group, "version": version, "kind": kind},
			},
		}
	}

	return map[string]any{
		"openapi":    "3.0.0",
		"info":       map[string]any{"title": "cloudemu-kubernetes", "version": "v1.29.0-cloudemu"},
		"paths":      map[string]any{},
		"components": map[string]any{"schemas": schemas},
	}
}

// parseGVPath turns "api/v1" into ("","v1") and "apis/<group>/<version>" into
// ("<group>","<version>").
func parseGVPath(p string) (group, version string) {
	parts := strings.Split(p, "/")

	switch {
	case len(parts) == 2 && parts[0] == pathSegAPI:
		return "", parts[1]
	case len(parts) == 3 && parts[0] == pathSegAPIs:
		return parts[1], parts[2]
	}

	return "", ""
}

// kindsForGroupVersion returns the distinct kinds the server serves in a
// group-version, reusing the same source discovery is built from so the two
// can't drift.
func kindsForGroupVersion(group, version string) []string {
	var res []apiResource

	switch {
	case group == "" && version == apiVersionV1:
		res = coreResources()
	case group == apiGroupApps && version == apiVersionV1:
		res = appsResources()
	case group == apiGroupPolicy && version == apiVersionV1:
		res = policyResources()
	default:
		res = registryAPIResources(group, version)
	}

	seen := map[string]bool{}

	var kinds []string

	for _, r := range res {
		// Skip subresources (name contains '/', e.g. deployments/scale) — they
		// share their parent's Kind and aren't separately validated.
		if strings.Contains(r.Name, "/") || seen[r.Kind] {
			continue
		}

		seen[r.Kind] = true

		kinds = append(kinds, r.Kind)
	}

	return kinds
}

func schemaKey(group, version, kind string) string {
	if group == "" {
		return "io.k8s.api.core." + version + "." + kind
	}

	return "io.k8s.api." + group + "." + version + "." + kind
}

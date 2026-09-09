// Package kms implements the cloudkms.googleapis.com v1 REST control plane as a
// server.Handler. Real google.golang.org/api/cloudkms/v1 and
// cloud.google.com/go/kms clients, gcloud, and the Terraform google provider
// pointed at this server CRUD key rings, crypto keys, and crypto-key versions
// end-to-end.
//
// Coverage (v1 REST — control plane):
//
//	POST   /v1/projects/{p}/locations/{l}/keyRings?keyRingId={id}                       — Create key ring
//	GET    /v1/projects/{p}/locations/{l}/keyRings/{r}                                  — Get key ring
//	GET    /v1/projects/{p}/locations/{l}/keyRings                                      — List key rings
//	POST   .../keyRings/{r}/cryptoKeys?cryptoKeyId={id}&skipInitialVersionCreation=     — Create crypto key
//	GET    .../keyRings/{r}/cryptoKeys/{k}                                              — Get crypto key
//	GET    .../keyRings/{r}/cryptoKeys                                                  — List crypto keys
//	PATCH  .../keyRings/{r}/cryptoKeys/{k}?updateMask=                                  — Patch crypto key
//	POST   .../cryptoKeys/{k}:updatePrimaryVersion                                      — Update primary version
//	POST   .../cryptoKeys/{k}/cryptoKeyVersions                                         — Create version
//	GET    .../cryptoKeyVersions/{v}                                                    — Get version
//	GET    .../cryptoKeys/{k}/cryptoKeyVersions                                         — List versions
//	PATCH  .../cryptoKeyVersions/{v}?updateMask=                                        — Patch version (state)
//	POST   .../cryptoKeyVersions/{v}:destroy                                            — Schedule destruction
//	POST   .../cryptoKeyVersions/{v}:restore                                            — Restore scheduled version
//	{GET,POST} .../{keyRings/{r}|.../cryptoKeys/{k}}:{get,set}IamPolicy/:testIamPermissions
//
// keyRings.create, cryptoKeys.create and cryptoKeyVersions.create are
// synchronous — they return the resource directly, not a long-running
// operation. On cryptoKeys.create without skipInitialVersionCreation the
// handler auto-creates version 1 in state ENABLED (and sets it primary for
// ENCRYPT_DECRYPT keys), matching real Cloud KMS. Version destruction is a
// state transition to DESTROY_SCHEDULED — the version, key and ring persist.
//
// The data plane (Encrypt/Decrypt/Sign/Verify/MAC/GenerateRandomBytes), import
// jobs, EKM/external keys and Autokey are out of scope for this control-plane
// build; new versions go straight to ENABLED (no async PENDING_GENERATION).
package kms

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	pathPrefix        = "/v1/projects/"
	keyRingsSeg       = "keyRings"
	cryptoKeysSeg     = "cryptoKeys"
	versionsSeg       = "cryptoKeyVersions"
	projectsSeg       = "projects"
	locationsSeg      = "locations"
	minHeadParts      = 5 // [projects, {p}, locations, {l}, keyRings]
	keyRingsHeadIndex = 4

	verbUpdatePrimary = "updatePrimaryVersion"
	verbDestroy       = "destroy"
	verbRestore       = "restore"
	verbGetIam        = "getIamPolicy"
	verbSetIam        = "setIamPolicy"
	verbTestIam       = "testIamPermissions"
)

// routeKind identifies which Cloud KMS endpoint a path addresses.
type routeKind int

const (
	kindKeyRingColl routeKind = iota
	kindKeyRing
	kindCryptoKeyColl
	kindCryptoKey
	kindVersionColl
	kindVersion
)

type route struct {
	project, location           string
	keyRing, cryptoKey, version string
	verb                        string
	kind                        routeKind
}

// Handler serves cloudkms.googleapis.com v1 control-plane requests.
type Handler struct {
	store *store
}

// New returns a Cloud KMS handler. clock stamps createTime/destroyTime; pass a
// config.FakeClock for deterministic tests, or nil for the real clock.
func New(clock config.Clock) *Handler {
	return &Handler{store: newStore(clock)}
}

// Path-tail depths after the [projects, {p}, locations, {l}, keyRings] head:
// the number of segments following the keyRings segment.
const (
	depthKeyRing       = 1 // keyRings/{r}
	depthCryptoKeyColl = 2 // keyRings/{r}/cryptoKeys
	depthCryptoKey     = 3 // keyRings/{r}/cryptoKeys/{k}
	depthVersionColl   = 4 // .../cryptoKeys/{k}/cryptoKeyVersions
	depthVersion       = 5 // .../cryptoKeyVersions/{v}
)

// parseRoute decomposes a Cloud KMS v1 path. The trailing segment may carry a
// ":verb" suffix.
func parseRoute(urlPath string) (*route, bool) {
	// terraform-provider-google's crypto-key delete lists the key's versions via
	// a URL that doubles the version prefix (".../v1/v1/projects/...") when the
	// KMS endpoint is overridden, so a real `terraform destroy` of a crypto key
	// would 404 here. Collapse the redundant leading segment before matching.
	urlPath = strings.Replace(urlPath, "/v1/v1/", "/v1/", 1)

	if !strings.HasPrefix(urlPath, pathPrefix) {
		return nil, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minHeadParts || parts[0] != projectsSeg ||
		parts[2] != locationsSeg || parts[keyRingsHeadIndex] != keyRingsSeg {
		return nil, false
	}

	rt := &route{project: parts[1], location: parts[3]}
	if !fillRoute(rt, parts[minHeadParts:]) {
		return nil, false
	}

	return rt, true
}

// fillRoute assigns the resource ids from the path tail after the keyRings
// segment and classifies the endpoint kind.
//
//nolint:gocyclo // flat switch over the six fixed KMS path depths; each arm trivial.
func fillRoute(rt *route, rest []string) bool {
	switch len(rest) {
	case 0:
		rt.kind = kindKeyRingColl
	case depthKeyRing:
		rt.keyRing, rt.verb, _ = strings.Cut(rest[0], ":")
		rt.kind = kindKeyRing
	case depthCryptoKeyColl:
		if rest[1] != cryptoKeysSeg {
			return false
		}

		rt.keyRing, rt.kind = rest[0], kindCryptoKeyColl
	case depthCryptoKey:
		if rest[1] != cryptoKeysSeg {
			return false
		}

		rt.keyRing = rest[0]
		rt.cryptoKey, rt.verb, _ = strings.Cut(rest[2], ":")
		rt.kind = kindCryptoKey
	case depthVersionColl:
		if rest[1] != cryptoKeysSeg || rest[3] != versionsSeg {
			return false
		}

		rt.keyRing, rt.cryptoKey, rt.kind = rest[0], rest[2], kindVersionColl
	case depthVersion:
		if rest[1] != cryptoKeysSeg || rest[3] != versionsSeg {
			return false
		}

		rt.keyRing, rt.cryptoKey = rest[0], rest[2]
		rt.version, rt.verb, _ = strings.Cut(rest[4], ":")
		rt.kind = kindVersion
	default:
		return false
	}

	return true
}

// Matches claims /v1/projects/{p}/locations/{l}/keyRings[...] paths. Its
// keyRings resource-type guard is disjoint from every other /v1/projects/
// handler (memorystore's instances, GKE's clusters, cloudfunctions' functions,
// eventarc's triggers, scheduler's jobs, vertexai, artifactregistry's
// repositories), so registration order among them is unconstrained — but it
// must precede Firestore's permissive /v1/projects/ prefix.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	return ok && rt.project != "" && rt.location != ""
}

// ServeHTTP routes on the parsed endpoint kind.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud KMS path")
		return
	}

	switch rt.kind {
	case kindKeyRingColl:
		h.serveKeyRingCollection(w, r, rt)
	case kindKeyRing:
		h.serveKeyRing(w, r, rt)
	case kindCryptoKeyColl:
		h.serveCryptoKeyCollection(w, r, rt)
	case kindCryptoKey:
		h.serveCryptoKey(w, r, rt)
	case kindVersionColl:
		h.serveVersionCollection(w, r, rt)
	case kindVersion:
		h.serveVersion(w, r, rt)
	}
}

func (h *Handler) serveKeyRingCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createKeyRing(w, r, rt)
	case http.MethodGet:
		h.listKeyRings(w, rt)
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) serveKeyRing(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.verb != "" {
		h.serveIamVerb(w, r, rt)
		return
	}

	if r.Method == http.MethodGet {
		h.getKeyRing(w, rt)
		return
	}

	writeUnsupported(w)
}

func (h *Handler) serveCryptoKeyCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createCryptoKey(w, r, rt)
	case http.MethodGet:
		h.listCryptoKeys(w, rt)
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) serveCryptoKey(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.verb {
	case "":
		h.serveCryptoKeyNoVerb(w, r, rt)
	case verbUpdatePrimary:
		postOnly(w, r, func() { h.updatePrimaryVersion(w, r, rt) })
	default:
		h.serveIamVerb(w, r, rt)
	}
}

func (h *Handler) serveCryptoKeyNoVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getCryptoKey(w, rt)
	case http.MethodPatch:
		h.patchCryptoKey(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) serveVersionCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createVersion(w, r, rt)
	case http.MethodGet:
		h.listVersions(w, rt)
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) serveVersion(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.verb {
	case "":
		h.serveVersionNoVerb(w, r, rt)
	case verbDestroy:
		postOnly(w, r, func() { h.destroyVersion(w, rt) })
	case verbRestore:
		postOnly(w, r, func() { h.restoreVersion(w, rt) })
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) serveVersionNoVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getVersion(w, rt)
	case http.MethodPatch:
		h.patchVersion(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveIamVerb dispatches the IAM policy custom methods shared by keyRings and
// cryptoKeys.
func (h *Handler) serveIamVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.verb {
	case verbGetIam:
		getOnly(w, r, func() { h.getIamPolicy(w, rt) })
	case verbSetIam:
		postOnly(w, r, func() { h.setIamPolicy(w, r, rt) })
	case verbTestIam:
		postOnly(w, r, func() { h.testIamPermissions(w, r) })
	default:
		writeUnsupported(w)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Cloud KMS operation")
}

func getOnly(w http.ResponseWriter, r *http.Request, fn func()) {
	if r.Method == http.MethodGet {
		fn()
		return
	}

	writeUnsupported(w)
}

func postOnly(w http.ResponseWriter, r *http.Request, fn func()) {
	if r.Method == http.MethodPost {
		fn()
		return
	}

	writeUnsupported(w)
}

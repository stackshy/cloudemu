// Package objectstorage implements OCI's Object Storage REST API against a
// CloudEmu storage driver. Real github.com/oracle/oci-go-sdk objectstorage
// clients hit this handler the same way they hit
// objectstorage.<region>.oraclecloud.com.
//
// Object Storage carries no API-version prefix; every path is rooted at the
// tenancy namespace, so Matches claims /n and the pre-authenticated request
// redemption prefix and nothing else:
//
//	GET                  /n                                        — get namespace
//	GET                  /n/{ns}                                   — namespace metadata
//	POST/GET             /n/{ns}/b                                 — create, list (by compartmentId)
//	GET/POST/DELETE/HEAD /n/{ns}/b/{bucket}                        — get, update, delete, head
//	GET                  /n/{ns}/b/{bucket}/o                      — list objects
//	PUT/GET/HEAD/DELETE  /n/{ns}/b/{bucket}/o/{object}             — put, get, head, delete
//	GET                  /n/{ns}/b/{bucket}/objectversions         — list object versions
//	POST                 /n/{ns}/b/{bucket}/actions/renameObject
//	POST                 /n/{ns}/b/{bucket}/actions/copyObject     — async, work request
//	POST                 /n/{ns}/b/{bucket}/actions/updateObjectStorageTier
//	POST/GET             /n/{ns}/b/{bucket}/u                      — multipart create, list
//	PUT/POST/GET/DELETE  /n/{ns}/b/{bucket}/u/{object}             — upload, commit, list parts, abort
//	POST/GET             /n/{ns}/b/{bucket}/p                      — pre-authenticated requests
//	GET/DELETE           /n/{ns}/b/{bucket}/p/{parId}
//	POST/GET             /n/{ns}/b/{bucket}/retentionRules[/{id}]
//	PUT/GET/DELETE       /n/{ns}/b/{bucket}/l                      — object lifecycle policy
//	GET/PUT              /p/{par}/n/{ns}/b/{bucket}/o/{object}     — redeem a PAR
//
// Only ListBuckets requires compartmentId: it is the one collection OCI scopes
// by compartment. Every other list here is scoped by the bucket, which already
// belongs to a compartment, so requiring the parameter would reject calls real
// OCI accepts.
//
// Not emulated: /actions/reencrypt and /actions/restoreObjects, which need
// per-object key material and an archive-retrieval lifecycle the storage driver
// has no shape for — the handler claims them so a caller is told why rather
// than left with a bare 404.
package objectstorage

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Path segments this handler claims.
const (
	segNamespace = "n"
	segPAR       = "p"
	segBuckets   = "b"

	subObjects        = "o"
	subUploads        = "u"
	subPARs           = "p"
	subActions        = "actions"
	subRetentionRules = "retentionRules"
	subObjectVersions = "objectversions"
	subLifecycle      = "l"
)

// Actions on a bucket.
const (
	actionRename         = "renameObject"
	actionCopy           = "copyObject"
	actionUpdateTier     = "updateObjectStorageTier"
	actionReencrypt      = "reencrypt"
	actionRestoreObjects = "restoreObjects"
)

// Error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
	codeNotAuthorized    = "NotAuthenticated"
)

// operationCopy is the work request a copyObject records.
const operationCopy = "COPY_OBJECT"

// Extras is the OCI-only surface the portable storage driver cannot express:
// the tenancy namespace, compartments, OCI's bucket settings, object rename
// and storage tiers, retention rules and pre-authenticated requests.
// *providers/oci/objectstorage.Mock satisfies it; any driver that does not is
// served 501 for every path this handler claims.
type Extras interface {
	Namespace() string
	Metadata(ctx context.Context) osprovider.NamespaceMetadata
	Scope(bucket string) scope.Scope

	CreateBucketWith(ctx context.Context, spec osprovider.BucketSpec) (*osprovider.Bucket, error)
	BucketDetails(ctx context.Context, name string) (*osprovider.Bucket, error)
	UpdateBucket(ctx context.Context, name string, upd osprovider.BucketUpdate) (*osprovider.Bucket, error)
	ListBucketsIn(ctx context.Context, compartmentID string) ([]osprovider.Bucket, error)

	PutObjectWith(
		ctx context.Context, bucket, key string, data []byte, opts osprovider.PutOptions,
	) (*osprovider.ObjectDetails, error)
	ObjectDetailsOf(ctx context.Context, bucket, key string) (*osprovider.ObjectDetails, error)
	ListObjectDetails(
		ctx context.Context, bucket string, opts driver.ListOptions,
	) ([]osprovider.ObjectDetails, []string, string, error)
	RenameObject(ctx context.Context, bucket, sourceName, newName string) (*osprovider.ObjectDetails, error)
	UpdateObjectStorageTier(ctx context.Context, bucket, key, tier string) error

	CreateMultipartUploadWith(
		ctx context.Context, bucket string, spec osprovider.MultipartUploadSpec,
	) (*driver.MultipartUpload, error)

	CreateRetentionRule(
		ctx context.Context, bucket string, spec osprovider.RetentionRuleSpec,
	) (*osprovider.RetentionRule, error)
	GetRetentionRule(ctx context.Context, bucket, ruleID string) (*osprovider.RetentionRule, error)
	ListRetentionRules(ctx context.Context, bucket string) ([]osprovider.RetentionRule, error)
	UpdateRetentionRule(
		ctx context.Context, bucket, ruleID string, spec osprovider.RetentionRuleSpec,
	) (*osprovider.RetentionRule, error)
	DeleteRetentionRule(ctx context.Context, bucket, ruleID string) error

	CreatePAR(ctx context.Context, bucket string, spec osprovider.PARSpec) (*osprovider.PreauthenticatedRequest, error)
	GetPAR(ctx context.Context, bucket, parID string) (*osprovider.PreauthenticatedRequest, error)
	ListPARs(ctx context.Context, bucket, objectNamePrefix string) ([]osprovider.PreauthenticatedRequest, error)
	DeletePAR(ctx context.Context, bucket, parID string) error
	ResolvePAR(ctx context.Context, token string) (*osprovider.PreauthenticatedRequest, error)

	DeleteLifecyclePolicy(ctx context.Context, bucket string) error
}

// Handler serves OCI Object Storage against a storage driver.
type Handler struct {
	store     driver.Bucket
	extras    Extras
	versioned driver.VersionedBucket
	work      *workrequest.Store
}

// New returns an Object Storage handler. work records the asynchronous copy;
// a nil store leaves that path unserved.
func New(b driver.Bucket, work *workrequest.Store) *Handler {
	extras, _ := b.(Extras)
	versioned, _ := b.(driver.VersionedBucket)

	return &Handler{store: b, extras: extras, versioned: versioned, work: work}
}

// route is a parsed Object Storage path.
type route struct {
	// PARToken is the redemption token of a /p/{token}/… request.
	PARToken  string
	Namespace string
	Bucket    string
	// Sub is the collection under a bucket: o, u, p, actions, retentionRules,
	// objectversions or l.
	Sub string
	// Rest is everything after Sub: an object name (which may contain slashes),
	// a PAR OCID, a retention rule OCID or an action name.
	Rest string
}

// Matches claims the namespace-rooted Object Storage paths and the PAR
// redemption prefix, and nothing else.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parsePath(r.URL.Path)

	return ok
}

// parsePath splits an Object Storage path. It accepts the namespace root and
// everything under it, plus a /p/{token}/n/… PAR redemption.
func parsePath(p string) (route, bool) {
	var rt route

	rest := p

	if strings.HasPrefix(rest, "/"+segPAR+"/") {
		rem := rest[len("/"+segPAR+"/"):]

		idx := strings.Index(rem, "/"+segNamespace+"/")
		if idx <= 0 {
			return rt, false
		}

		rt.PARToken = rem[:idx]
		rest = rem[idx:]
	}

	if rest == "/"+segNamespace || rest == "/"+segNamespace+"/" {
		return rt, true
	}

	if !strings.HasPrefix(rest, "/"+segNamespace+"/") {
		return rt, false
	}

	return parseNamespaced(rt, rest[len("/"+segNamespace+"/"):])
}

// parseNamespaced parses everything after /n/: the namespace, then the bucket
// collection under it.
//
//nolint:gocritic // route is built up and returned by value; the caller owns it.
func parseNamespaced(rt route, rem string) (route, bool) {
	rt.Namespace, rem, _ = strings.Cut(rem, "/")
	if rem == "" {
		return rt, true
	}

	var seg string

	seg, rem, _ = strings.Cut(rem, "/")
	if seg != segBuckets {
		return route{}, false
	}

	if rem == "" {
		return rt, true
	}

	rt.Bucket, rem, _ = strings.Cut(rem, "/")
	if rem == "" {
		return rt, true
	}

	rt.Sub, rt.Rest, _ = strings.Cut(rem, "/")

	return rt, true
}

// ServeHTTP routes on the path shape, then on method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed Object Storage path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired storage driver does not implement OCI namespaces and compartments")

		return
	}

	if rt.PARToken != "" {
		h.servePAR(w, r, &rt)
		return
	}

	if rt.Namespace == "" {
		h.getNamespace(w, r)
		return
	}

	if !h.namespaceOK(w, r, rt.Namespace) {
		return
	}

	if rt.Bucket == "" {
		h.serveBucketCollection(w, r, &rt)
		return
	}

	h.serveBucket(w, r, &rt)
}

// namespaceOK rejects a namespace that is not this tenancy's. Real OCI reports
// the same 404 it reports for a missing bucket.
func (h *Handler) namespaceOK(w http.ResponseWriter, r *http.Request, namespace string) bool {
	if namespace == h.extras.Namespace() {
		return true
	}

	ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "namespace "+namespace+" not found")

	return false
}

// serveBucketCollection serves /n/{ns} and /n/{ns}/b.
func (h *Handler) serveBucketCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	if !strings.Contains(r.URL.Path, "/"+segBuckets) {
		h.namespaceMetadata(w, r)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.createBucket(w, r)
	case http.MethodGet:
		h.listBuckets(w, r)
	default:
		methodNotAllowed(w, r)
	}

	_ = rt
}

// serveBucket dispatches everything addressed at one bucket.
func (h *Handler) serveBucket(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.Sub {
	case "":
		h.serveBucketItem(w, r, rt.Bucket)
	case subObjects:
		h.serveObjects(w, r, rt)
	case subUploads:
		h.serveUploads(w, r, rt)
	case subPARs:
		h.servePARs(w, r, rt)
	case subActions:
		h.serveAction(w, r, rt)
	case subRetentionRules:
		h.serveRetentionRules(w, r, rt)
	case subObjectVersions:
		h.listObjectVersions(w, r, rt.Bucket)
	case subLifecycle:
		h.serveLifecycle(w, r, rt.Bucket)
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown collection "+rt.Sub)
	}
}

// serveAction dispatches the bucket-level actions.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	switch rt.Rest {
	case actionRename:
		h.renameObject(w, r, rt.Bucket)
	case actionCopy:
		h.copyObject(w, r, rt.Bucket)
	case actionUpdateTier:
		h.updateStorageTier(w, r, rt.Bucket)
	case actionReencrypt:
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"reencrypt is not emulated; CloudEmu holds no per-object key material to re-wrap")
	case actionRestoreObjects:
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"restoreObjects is not emulated; archived objects are readable directly")
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+rt.Rest)
	}
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed,
		r.Method+" is not allowed on "+r.URL.Path)
}

// errVersioningUnsupported reports a driver that keeps no version history.
func errVersioningUnsupported() error {
	return cerrors.New(cerrors.Unimplemented, "the wired storage driver does not retain object versions")
}

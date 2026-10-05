package objectstorage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// parTokenBytes is the entropy behind a redemption token. The token is the
// only secret a PAR carries, so it comes from crypto/rand rather than the
// process counter, which restarts after a persist restore.
const parTokenBytes = 48

// defaultPresignLifetime is the lifetime GeneratePresignedURL gives a PAR when
// the portable caller names none; OCI itself requires timeExpires.
const defaultPresignLifetime = 7 * hoursPerDay * time.Hour

// PreauthenticatedRequest is OCI's presigned-URL equivalent: a first-class
// resource with its own OCID and lifetime, listable and revocable, rather than
// a signature baked into a URL.
type PreauthenticatedRequest struct {
	ID                  string
	Name                string
	Bucket              string
	ObjectName          string
	AccessType          string
	BucketListingAction string
	TimeCreated         string
	TimeExpires         string
	// AccessURI is the path the request is redeemed at. Real OCI returns it
	// only from CreatePreauthenticatedRequest, never from a later Get.
	AccessURI string
}

// PARSpec is a pre-authenticated request to create.
type PARSpec struct {
	Name                string
	ObjectName          string
	AccessType          string
	BucketListingAction string
	TimeExpires         time.Time
}

type parData struct {
	ID                  string
	Name                string
	Bucket              string
	ObjectName          string
	AccessType          string
	BucketListingAction string
	TimeCreated         string
	TimeExpires         time.Time
	token               string
}

func validPARAccess(v string) bool {
	switch v {
	case PARObjectRead, PARObjectWrite, PARObjectReadWrite,
		PARAnyObjectRead, PARAnyObjectWrite, PARAnyObjectReadWrite:
		return true
	}

	return false
}

// parScopedToObject reports whether an access type binds the request to a
// single named object rather than the whole bucket.
func parScopedToObject(accessType string) bool {
	switch accessType {
	case PARObjectRead, PARObjectWrite, PARObjectReadWrite:
		return true
	}

	return false
}

// CreatePAR creates a pre-authenticated request against a bucket or one of its
// objects.
//
//nolint:gocritic // PARSpec is a request shape, passed by value like the driver's own config structs.
func (m *Mock) CreatePAR(_ context.Context, bucket string, spec PARSpec) (*PreauthenticatedRequest, error) {
	if spec.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "name is required")
	}

	if !validPARAccess(spec.AccessType) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported accessType %q", spec.AccessType)
	}

	if parScopedToObject(spec.AccessType) && spec.ObjectName == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "objectName is required for accessType %q", spec.AccessType)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now().UTC()

	expires, err := parExpiry(spec.TimeExpires, now)
	if err != nil {
		return nil, err
	}

	token, err := m.newPARTokenLocked()
	if err != nil {
		return nil, err
	}

	par := &parData{
		ID:                  m.newPARIDLocked(),
		Name:                spec.Name,
		Bucket:              bucket,
		ObjectName:          spec.ObjectName,
		AccessType:          spec.AccessType,
		BucketListingAction: spec.BucketListingAction,
		TimeCreated:         now.Format(timeFormat),
		TimeExpires:         expires,
		token:               token,
	}

	bkt.pars.Set(par.ID, par)

	out := projectPAR(par)
	out.AccessURI = m.accessURI(par)

	return out, nil
}

// parExpiry validates timeExpires. OCI requires one and caps it only at the
// far future, so a PAR a year or more out is accepted.
func parExpiry(requested, now time.Time) (time.Time, error) {
	if requested.IsZero() {
		return time.Time{}, cerrors.New(cerrors.InvalidArgument, "timeExpires is required")
	}

	expires := requested.UTC()
	if !expires.After(now) {
		return time.Time{}, cerrors.New(cerrors.InvalidArgument, "timeExpires must be in the future")
	}

	return expires, nil
}

// newPARTokenLocked mints a redemption token no live PAR holds. Callers hold mu.
func (m *Mock) newPARTokenLocked() (string, error) {
	buf := make([]byte, parTokenBytes)

	for {
		if _, err := rand.Read(buf); err != nil {
			return "", cerrors.Newf(cerrors.Internal, "generate pre-authenticated request token: %v", err)
		}

		token := base64.RawURLEncoding.EncodeToString(buf)
		if m.findPARLocked(func(p *parData) bool { return p.token == token }) == nil {
			return token, nil
		}
	}
}

// newPARIDLocked mints a PAR OCID no live PAR holds. The OCID counter restarts
// after a persist restore while restored PARs keep theirs. Callers hold mu.
func (m *Mock) newPARIDLocked() string {
	for {
		id := idgen.OCID(typePAR, m.opts.Realm, m.opts.OCIRegion())
		if m.findPARLocked(func(p *parData) bool { return p.ID == id }) == nil {
			return id
		}
	}
}

// findPARLocked returns the first PAR in any bucket matching pred. Callers
// hold mu.
func (m *Mock) findPARLocked(pred func(*parData) bool) *parData {
	for _, name := range m.buckets.Keys() {
		bkt, ok := m.buckets.Get(name)
		if !ok {
			continue
		}

		for _, id := range bkt.pars.Keys() {
			if par, exists := bkt.pars.Get(id); exists && pred(par) {
				return par
			}
		}
	}

	return nil
}

// accessURI is the path a PAR is redeemed at, matching the shape real OCI
// returns: /p/{token}/n/{namespace}/b/{bucket}/o/{object}.
func (m *Mock) accessURI(par *parData) string {
	uri := "/p/" + par.token + "/n/" + m.namespace + "/b/" + par.Bucket + "/o/"
	if parScopedToObject(par.AccessType) {
		uri += par.ObjectName
	}

	return uri
}

// GetPAR returns one pre-authenticated request. Its access URI is not
// repeated, as real OCI does not repeat it either.
func (m *Mock) GetPAR(_ context.Context, bucket, parID string) (*PreauthenticatedRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	par, ok := bkt.pars.Get(parID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "pre-authenticated request %q not found in bucket %q", parID, bucket)
	}

	return projectPAR(par), nil
}

// ListPARs returns a bucket's pre-authenticated requests, ordered by id and
// optionally filtered to those whose name starts with prefix.
func (m *Mock) ListPARs(_ context.Context, bucket, objectNamePrefix string) ([]PreauthenticatedRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	ids := bkt.pars.Keys()
	sort.Strings(ids)

	out := make([]PreauthenticatedRequest, 0, len(ids))

	for _, id := range ids {
		par, ok := bkt.pars.Get(id)
		if !ok {
			continue
		}

		if objectNamePrefix != "" && !hasPrefix(par.ObjectName, objectNamePrefix) {
			continue
		}

		out = append(out, *projectPAR(par))
	}

	return out, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// DeletePAR revokes a pre-authenticated request.
func (m *Mock) DeletePAR(_ context.Context, bucket, parID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	if !bkt.pars.Delete(parID) {
		return cerrors.Newf(cerrors.NotFound, "pre-authenticated request %q not found in bucket %q", parID, bucket)
	}

	return nil
}

// ResolvePAR resolves a redemption token to the request it authorizes,
// refusing one that has expired. It is what makes the access URI usable rather
// than decorative.
func (m *Mock) ResolvePAR(_ context.Context, token string) (*PreauthenticatedRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	par := m.findPARLocked(func(p *parData) bool { return p.token == token })
	if par == nil {
		return nil, cerrors.New(cerrors.NotFound, "pre-authenticated request not found")
	}

	if !m.opts.Clock.Now().UTC().Before(par.TimeExpires) {
		return nil, cerrors.Newf(cerrors.PermissionDenied, "pre-authenticated request %q has expired", par.ID)
	}

	return projectPAR(par), nil
}

// parGrantsRead and parGrantsWrite report which verb an access type grants.
func parGrantsRead(accessType string) bool {
	switch accessType {
	case PARObjectRead, PARObjectReadWrite, PARAnyObjectRead, PARAnyObjectReadWrite:
		return true
	}

	return false
}

func parGrantsWrite(accessType string) bool {
	switch accessType {
	case PARObjectWrite, PARObjectReadWrite, PARAnyObjectWrite, PARAnyObjectReadWrite:
		return true
	}

	return false
}

// PARAllows reports whether a resolved request authorizes method on object.
// An object-scoped PAR names one object; on the AnyObject types objectName is a
// prefix, as OCI treats it.
func PARAllows(par *PreauthenticatedRequest, method, object string) bool {
	if parScopedToObject(par.AccessType) {
		if par.ObjectName != object {
			return false
		}
	} else if !strings.HasPrefix(object, par.ObjectName) {
		return false
	}

	switch method {
	case http.MethodGet, http.MethodHead:
		return parGrantsRead(par.AccessType)
	case http.MethodPut:
		return parGrantsWrite(par.AccessType)
	default:
		return false
	}
}

func projectPAR(par *parData) *PreauthenticatedRequest {
	return &PreauthenticatedRequest{
		ID:                  par.ID,
		Name:                par.Name,
		Bucket:              par.Bucket,
		ObjectName:          par.ObjectName,
		AccessType:          par.AccessType,
		BucketListingAction: par.BucketListingAction,
		TimeCreated:         par.TimeCreated,
		TimeExpires:         par.TimeExpires.UTC().Format(timeFormat),
	}
}

// GeneratePresignedURL is OCI's pre-authenticated request behind the portable
// name: it creates a real PAR resource and returns its access URI. A caller
// that wants to list or revoke it later uses ListPARs and DeletePAR.
func (m *Mock) GeneratePresignedURL(ctx context.Context, req driver.PresignedURLRequest) (*driver.PresignedURL, error) {
	var accessType string

	switch req.Method {
	case http.MethodGet:
		accessType = PARObjectRead
	case http.MethodPut:
		accessType = PARObjectWrite
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "method must be GET or PUT, got %q", req.Method)
	}

	expiresIn := req.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = defaultPresignLifetime
	}

	expires := m.opts.Clock.Now().UTC().Add(expiresIn)

	par, err := m.CreatePAR(ctx, req.Bucket, PARSpec{
		Name:        "presigned-" + req.Key,
		ObjectName:  req.Key,
		AccessType:  accessType,
		TimeExpires: expires,
	})
	if err != nil {
		return nil, err
	}

	return &driver.PresignedURL{
		URL:       "https://objectstorage." + m.opts.OCIRegion() + ".oraclecloud.com" + par.AccessURI,
		Method:    req.Method,
		ExpiresAt: expires,
	}, nil
}

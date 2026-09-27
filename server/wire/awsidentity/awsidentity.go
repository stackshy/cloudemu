// Package awsidentity works out which AWS principal sent a request. STS uses
// it for GetCallerIdentity and to remember the sessions it mints. Other
// handlers, such as EKS for the cluster creator, share the same Resolver so
// they see the caller the way STS reports it.
package awsidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// defaultAccountID is the account used when none is configured. STS falls
// back to the same one.
const defaultAccountID = "000000000000"

// defaultUserName is the IAM user reported for a request with no credentials.
const defaultUserName = "cloudemu"

// defaultUserID is the unique id reported for a request with no credentials.
const defaultUserID = "AIDACLOUDEMU0000000000"

// syntheticUserIDHexLen is the number of hex characters of a SHA-256 digest
// of the access key id that follow the AIDA prefix. Real IAM unique ids have
// the same length.
const syntheticUserIDHexLen = 16

// Identity is the Arn and UserId pair GetCallerIdentity reports.
type Identity struct {
	ARN    string
	UserID string
}

// Resolver resolves the caller of a request. It is safe for concurrent use.
type Resolver struct {
	accountID string
	keys      iamdriver.AccessKeyResolver

	mu sync.RWMutex
	// minted maps a temporary access key id STS issued to the identity it
	// stands for.
	minted map[string]Identity
}

// New returns a Resolver for accountID. When iam can map access keys to
// users (iamdriver.AccessKeyResolver), long-term keys resolve to the IAM user
// that owns them. iam may be nil.
func New(accountID string, iam iamdriver.IAM) *Resolver {
	if accountID == "" {
		accountID = defaultAccountID
	}

	keys, _ := iam.(iamdriver.AccessKeyResolver)

	return &Resolver{accountID: accountID, keys: keys, minted: make(map[string]Identity)}
}

// Remember records the identity a temporary access key id stands for. A blank
// key or an empty identity is ignored.
func (r *Resolver) Remember(accessKeyID string, id Identity) {
	if accessKeyID == "" || (id.ARN == "" && id.UserID == "") {
		return
	}

	r.mu.Lock()
	r.minted[accessKeyID] = id
	r.mu.Unlock()
}

func (r *Resolver) lookup(accessKeyID string) (Identity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.minted[accessKeyID]

	return id, ok
}

// Resolve returns the caller of req, in this order:
//
//  1. A principal the SigV4 authentication gate verified (EnforceAuth on).
//  2. No credentials at all: the default identity.
//  3. A temporary access key id STS minted: the identity recorded for it.
//  4. A long-term access key id the IAM driver knows: the user that owns it.
//  5. Otherwise a stable identity derived from the access key id, so two
//     callers never collapse onto one fake identity.
//
// Outside EnforceAuth the signature is not checked, so this is who the
// request claims to be. That matches what GetCallerIdentity reflects.
func (r *Resolver) Resolve(req *http.Request) Identity {
	if p, ok := authctx.PrincipalFrom(req.Context()); ok && p.ARN != "" {
		return Identity{ARN: p.ARN, UserID: firstNonEmpty(p.UserID, syntheticUserID(p.AccessKeyID))}
	}

	akid := sigv4.AccessKeyID(req)
	if akid == "" {
		return r.defaultIdentity()
	}

	if id, ok := r.lookup(akid); ok {
		return id
	}

	if r.keys != nil {
		if info, ok := r.keys.AccessKeyByID(req.Context(), akid); ok && info.UserARN != "" {
			return Identity{ARN: info.UserARN, UserID: firstNonEmpty(info.UserID, syntheticUserID(akid))}
		}
	}

	return r.synthetic(akid)
}

// defaultIdentity is the identity of a request that carries no credentials.
func (r *Resolver) defaultIdentity() Identity {
	return Identity{ARN: "arn:aws:iam::" + r.accountID + ":user/" + defaultUserName, UserID: defaultUserID}
}

// synthetic derives a stable identity from an access key id nothing else
// could resolve.
func (r *Resolver) synthetic(akid string) Identity {
	return Identity{
		ARN:    "arn:aws:iam::" + r.accountID + ":user/" + strings.ReplaceAll(akid, "/", "-"),
		UserID: syntheticUserID(akid),
	}
}

// syntheticUserID derives an AIDA style unique id from an access key id. The
// same key always gives the same id.
func syntheticUserID(akid string) string {
	if akid == "" {
		return defaultUserID
	}

	sum := sha256.Sum256([]byte(akid))

	return "AIDA" + strings.ToUpper(hex.EncodeToString(sum[:]))[:syntheticUserIDHexLen]
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

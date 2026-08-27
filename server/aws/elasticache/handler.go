// Package elasticache implements the AWS ElastiCache query-protocol as a
// server.Handler. Point the real aws-sdk-go-v2 ElastiCache client at a Server
// registered with this handler and CacheCluster lifecycle operations
// (CreateCacheCluster / DescribeCacheClusters / DeleteCacheCluster) work
// against the in-memory cache driver.
//
// ElastiCache shares the AWS query wire shape with EC2, RDS, Redshift, and IAM
// (POST + form-encoded body, XML response). To keep dispatch unambiguous, this
// handler's Matches predicate parses the form body once and only claims
// requests whose Action is one of the known ElastiCache operations. The EC2
// handler is the catch-all for all other query-protocol actions, so this
// handler MUST register before EC2. Its action set (CreateCacheCluster, …) is
// disjoint from RDS (CreateDBInstance, …), Redshift (CreateCluster, …), IAM
// (CreateUser, …), and EC2 (RunInstances, …), so no shadowing occurs.
//
// Only the cluster/instance control plane is mapped here — the real ElastiCache
// SDK manages cache clusters, not the Redis data plane. The driver's Redis
// data-plane methods (Set/Get/Incr/…) have no cloud-SDK surface and are
// intentionally out of scope.
package elasticache

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// Namespace is the XML namespace for AWS ElastiCache responses.
const Namespace = "http://elasticache.amazonaws.com/doc/2015-02-02/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// elastiCacheActions is the set of Action values this handler recognizes.
// Matches uses it to decide whether to claim a request.
var elastiCacheActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateCacheSubnetGroup":       {},
	"DescribeCacheSubnetGroups":    {},
	"DeleteCacheSubnetGroup":       {},
	"CreateCacheCluster":           {},
	"DescribeCacheClusters":        {},
	"ModifyCacheCluster":           {},
	"DeleteCacheCluster":           {},
	"RebootCacheCluster":           {},
	"AddTagsToResource":            {},
	"ListTagsForResource":          {},
	"RemoveTagsFromResource":       {},
	"CreateCacheParameterGroup":    {},
	"DescribeCacheParameterGroups": {},
	"ModifyCacheParameterGroup":    {},
	"ResetCacheParameterGroup":     {},
	"DeleteCacheParameterGroup":    {},
	"DescribeCacheParameters":      {},
	"DescribeCacheEngineVersions":  {},
	"CreateReplicationGroup":       {},
	"DescribeReplicationGroups":    {},
	"ModifyReplicationGroup":       {},
	"DeleteReplicationGroup":       {},
	"CreateSnapshot":               {},
	"DescribeSnapshots":            {},
}

// sharedTagActions are the generic tag verbs ElastiCache shares with other
// query-protocol services on the same wire (RDS for all three; SNS also uses
// ListTagsForResource). Matches claims them only when the SigV4 credential
// scope names "elasticache", otherwise they fall through to the owning handler.
var sharedTagActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"AddTagsToResource":      {},
	"ListTagsForResource":    {},
	"RemoveTagsFromResource": {},
}

// sharedSnapshotActions are the snapshot verbs whose Action names collide with
// EC2's EBS snapshots (CreateSnapshot / DescribeSnapshots) on the same query
// wire. ElastiCache registers before EC2 (first-match-wins), so without a scope
// gate this handler would steal every EBS snapshot call. Matches claims them
// only when the SigV4 credential scope names "elasticache"; otherwise they fall
// through to the EC2 EBS handler.
var sharedSnapshotActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateSnapshot":    {},
	"DescribeSnapshots": {},
}

// scopeElastiCache is the SigV4 credential-scope service name for ElastiCache.
const scopeElastiCache = "elasticache"

// Handler serves ElastiCache query-protocol requests against a cache driver.
type Handler struct {
	cache cachedriver.Cache
}

// New returns an ElastiCache handler backed by c.
func New(c cachedriver.Cache) *Handler {
	return &Handler{cache: c}
}

// Matches returns true if the request looks like an AWS ElastiCache
// query-protocol call (POST + form-encoded body whose Action is one of the
// known ElastiCache operations). Calling ParseForm here caches the parsed form
// on the request so ServeHTTP can use it without re-reading the body.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return false
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		return false
	}

	action := r.Form.Get("Action")

	_, ok := elastiCacheActions[action]
	if !ok {
		return false
	}

	// The tag verbs are shared with RDS/SNS on the same query wire. Claim them
	// only when the SigV4 credential scope names "elasticache"; otherwise let
	// them fall through to the owning handler.
	if _, shared := sharedTagActions[action]; shared {
		return awsquery.CredentialScopeService(r.Header.Get("Authorization")) == scopeElastiCache
	}

	// The snapshot verbs collide with EC2's EBS snapshots. Claim them only when
	// the SigV4 credential scope names "elasticache"; an EC2-scoped call falls
	// through to the EC2 EBS handler.
	if _, shared := sharedSnapshotActions[action]; shared {
		return awsquery.CredentialScopeService(r.Header.Get("Authorization")) == scopeElastiCache
	}

	return true
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
//
//nolint:gocyclo,funlen // one case per action; the flat dispatch switch grows with the surface and reads clearer than sub-routers.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case "CreateCacheSubnetGroup":
		h.createCacheSubnetGroup(w, r)
	case "DescribeCacheSubnetGroups":
		h.describeCacheSubnetGroups(w, r)
	case "DeleteCacheSubnetGroup":
		h.deleteCacheSubnetGroup(w, r)
	case "CreateCacheCluster":
		h.createCacheCluster(w, r)
	case "ModifyCacheCluster":
		h.modifyCacheCluster(w, r)
	case "DescribeCacheClusters":
		h.describeCacheClusters(w, r)
	case "CreateReplicationGroup":
		h.createReplicationGroup(w, r)
	case "DescribeReplicationGroups":
		h.describeReplicationGroups(w, r)
	case "ModifyReplicationGroup":
		h.modifyReplicationGroup(w, r)
	case "DeleteReplicationGroup":
		h.deleteReplicationGroup(w, r)
	case "DeleteCacheCluster":
		h.deleteCacheCluster(w, r)
	case "RebootCacheCluster":
		h.rebootCacheCluster(w, r)
	case "AddTagsToResource":
		h.addTagsToResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
	case "RemoveTagsFromResource":
		h.removeTagsFromResource(w, r)
	case "CreateCacheParameterGroup":
		h.createCacheParameterGroup(w, r)
	case "DescribeCacheParameterGroups":
		h.describeCacheParameterGroups(w, r)
	case "ModifyCacheParameterGroup":
		h.modifyCacheParameterGroup(w, r)
	case "ResetCacheParameterGroup":
		h.resetCacheParameterGroup(w, r)
	case "DescribeCacheParameters":
		h.describeCacheParameters(w, r)
	case "DeleteCacheParameterGroup":
		h.deleteCacheParameterGroup(w, r)
	case "DescribeCacheEngineVersions":
		h.describeCacheEngineVersions(w, r)
	case "CreateSnapshot":
		h.createSnapshot(w, r)
	case "DescribeSnapshots":
		h.describeSnapshots(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown ElastiCache action: "+r.Form.Get("Action"))
	}
}

// writeErr maps cloudemu errors to ElastiCache XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, notFoundCode(err), msg)
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, alreadyExistsCode(err), msg)
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", msg)
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, failedPreconditionCode(err), msg)
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", msg)
	}
}

// failedPreconditionCode picks the AWS-shaped error code for a failed
// precondition. The driver encodes the intended fault as a prefix on the
// message (a subnet group still in use, or an unsupported snapshot); a caller
// matching the SDK's typed errors sees the code, not the message, so it has to
// be surfaced as the wire Code. Anything else is a cache-cluster state error.
func failedPreconditionCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "CacheSubnetGroupInUse"):
		return "CacheSubnetGroupInUse"
	case strings.Contains(msg, "SnapshotFeatureNotSupportedFault"):
		return "SnapshotFeatureNotSupportedFault"
	default:
		return "InvalidCacheClusterState"
	}
}

// notFoundCode picks the AWS-shaped error code for the resource the message
// names. A caller matching the SDK's typed errors sees the code, not the
// message, so a generic one leaves it unable to tell a missing replication
// group from a missing cache cluster.
//
// Order matters: "cache subnet group" also contains "cache", so the more
// specific resources are checked first.
func notFoundCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "snapshot"):
		return "SnapshotNotFoundFault"
	case strings.Contains(msg, "replication group"):
		return "ReplicationGroupNotFoundFault"
	case strings.Contains(msg, "cache subnet group"):
		return "CacheSubnetGroupNotFoundFault"
	case strings.Contains(msg, "parameter group"):
		return "CacheParameterGroupNotFound"
	default:
		return "CacheClusterNotFound"
	}
}

// alreadyExistsCode is the create-side counterpart of notFoundCode. Callers
// treat the specific already-exists code as "already provisioned, carry on",
// so collapsing it turns an idempotent re-run into a hard failure.
func alreadyExistsCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "snapshot"):
		return "SnapshotAlreadyExistsFault"
	case strings.Contains(msg, "replication group"):
		return "ReplicationGroupAlreadyExists"
	case strings.Contains(msg, "cache subnet group"):
		return "CacheSubnetGroupAlreadyExists"
	default:
		return "CacheClusterAlreadyExists"
	}
}

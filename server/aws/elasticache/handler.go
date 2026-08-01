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
	"CreateCacheSubnetGroup":    {},
	"DescribeCacheSubnetGroups": {},
	"DeleteCacheSubnetGroup":    {},
	"CreateCacheCluster":        {},
	"DescribeCacheClusters":     {},
	"DeleteCacheCluster":        {},
	"CreateReplicationGroup":    {},
	"DescribeReplicationGroups": {},
	"ModifyReplicationGroup":    {},
	"DeleteReplicationGroup":    {},
}

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

	_, ok := elastiCacheActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
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
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown ElastiCache action: "+r.Form.Get("Action"))
	}
}

// writeErr maps cloudemu errors to ElastiCache XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, notFoundCode(err), err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, alreadyExistsCode(err), err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidCacheClusterState", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
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
	case strings.Contains(msg, "replication group"):
		return "ReplicationGroupNotFoundFault"
	case strings.Contains(msg, "cache subnet group"):
		return "CacheSubnetGroupNotFoundFault"
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
	case strings.Contains(msg, "replication group"):
		return "ReplicationGroupAlreadyExists"
	case strings.Contains(msg, "cache subnet group"):
		return "CacheSubnetGroupAlreadyExists"
	default:
		return "CacheClusterAlreadyExists"
	}
}

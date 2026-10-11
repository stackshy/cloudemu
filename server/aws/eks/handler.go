// Package eks implements the AWS EKS REST/JSON control-plane API as a
// server.Handler. Point the real aws-sdk-go-v2/service/eks client at a
// Server registered with this handler and CreateCluster, CreateNodegroup,
// CreateFargateProfile, CreateAddon, and their friends work against an
// in-memory EKS driver.
//
// Wave 1 covers control-plane resources only; the Kubernetes data plane
// (Pods, Deployments, Services, …) is out of scope and is deferred to
// Wave 2. Until Wave 2 ships, the cluster Endpoint field returns a
// placeholder URL and the CertificateAuthority field returns a stub PEM,
// so kubeconfig generation works syntactically without a real apiserver.
//
// EKS uses REST/JSON (not the AWS query protocol). URL paths follow the
// shape the SDK emits, e.g. POST /clusters, POST
// /clusters/{name}/node-groups, POST /clusters/{name}/addons/{addon}/update.
// The handler's Matches predicate is rooted at /clusters, /tags/ and a few
// exact top-level GET paths so it does not shadow the catch-all S3 handler
// that may be registered alongside.
package eks

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/server/wire/awsidentity"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	pathPrefix = "/clusters"

	// tagsPrefix is the EKS tagging API root: /tags/{resourceArn}.
	tagsPrefix = "/tags/"

	// Top-level GET paths outside /clusters.
	pathAccessPolicies = "/access-policies"
	pathAddonVersions  = "/addons/supported-versions"
	pathAddonSchemas   = "/addons/configuration-schemas"

	// segNodeGroups, segFargateProfiles, segAddons are the EKS sub-resource
	// path segments. Real SDK kebab-cases them (note "node-groups" with a
	// hyphen; the JSON body field is camelCase "nodegroupName").
	segNodeGroups      = "node-groups"
	segFargateProfiles = "fargate-profiles"
	segAddons          = "addons"
	segUpdates         = "updates"
	segUpdateConfig    = "update-config"
	segUpdateVersion   = "update-version"
	segUpdate          = "update"
	segAccessEntries   = "access-entries"
	segAccessPolicies  = "access-policies"
)

// Path-segment counts the dispatcher branches on. Naming each one keeps the
// switch inside ServeHTTP free of magic numbers.
const (
	pathSegsCluster            = 1 // /clusters/{name}
	pathSegsClusterSubresource = 2 // /clusters/{name}/{action}
	pathSegsChildResource      = 3 // /clusters/{name}/{kind}/{child}
	pathSegsChildAction        = 4 // /clusters/{name}/{kind}/{child}/{action}
	pathSegsPolicyAssociation  = 5 // /clusters/{name}/access-entries/{arn}/access-policies/{policyArn}
)

// Handler serves AWS EKS REST/JSON requests against an EKS driver.
type Handler struct {
	eks eksdriver.EKS
	// identities, when set, resolves the cluster creator the way STS
	// GetCallerIdentity reports the caller.
	identities *awsidentity.Resolver
	// accountID and region are the account and region this handler serves.
	accountID, region string
}

// Option configures a Handler.
type Option func(*Handler)

// WithScope sets the account and region this handler serves. A tagging ARN
// of another account or region then names no resource here. Empty values skip
// that check.
func WithScope(accountID, region string) Option {
	return func(h *Handler) { h.accountID, h.region = accountID, region }
}

// New returns an EKS handler backed by the supplied driver.
func New(eks eksdriver.EKS, opts ...Option) *Handler {
	h := &Handler{eks: eks}
	for _, o := range opts {
		o(h)
	}

	return h
}

// SetIdentities wires the resolver CreateCluster uses to find the creator.
// Without one the creator comes from the verified principal or the access
// key alone, so sessions STS minted are not recognized.
func (h *Handler) SetIdentities(r *awsidentity.Resolver) { h.identities = r }

// Matches claims requests rooted at /clusters or /tags/, plus the exact
// GET paths for ListAccessPolicies, DescribeAddonVersions and
// DescribeAddonConfiguration. It rejects anything else so the catch-all S3
// handler can serve unrelated REST URLs without interference.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	switch p {
	case pathPrefix:
		return true
	case pathAccessPolicies, pathAddonVersions, pathAddonSchemas:
		return r.Method == http.MethodGet
	}

	return strings.HasPrefix(p, pathPrefix+"/") || strings.HasPrefix(p, tagsPrefix)
}

// ServeHTTP runs the operation classify names, or writes the error of a
// request it cannot name.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op, a := classify(r)

	for _, serve := range []func(http.ResponseWriter, *http.Request, opID, *opArgs) bool{
		h.serveClusterOp, h.serveNodegroupOp, h.serveFargateAddonOp, h.serveAccessOp, h.serveAccountOp,
	} {
		if serve(w, r, op, &a) {
			return
		}
	}

	h.writeUnknown(w, &a)
}

// serveClusterOp runs op when it is a cluster or update operation.
func (h *Handler) serveClusterOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opCreateCluster:
		h.createCluster(w, r)
	case opListClusters:
		h.listClusters(w, r)
	case opDescribeCluster:
		h.describeCluster(w, r, a.cluster)
	case opDeleteCluster:
		h.deleteCluster(w, r, a.cluster)
	case opUpdateClusterConfig:
		h.updateClusterConfig(w, r, a.cluster)
	case opUpdateClusterVersion:
		h.updateClusterVersion(w, r, a.cluster)
	case opListUpdates:
		h.listUpdates(w, r, a.cluster)
	case opDescribeUpdate:
		h.describeUpdate(w, r, a.cluster, a.child)
	default:
		return false
	}

	return true
}

// serveNodegroupOp runs op when it is a nodegroup operation.
func (h *Handler) serveNodegroupOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opCreateNodegroup:
		h.createNodegroup(w, r, a.cluster)
	case opListNodegroups:
		h.listNodegroups(w, r, a.cluster)
	case opDescribeNodegroup:
		h.describeNodegroup(w, r, a.cluster, a.child)
	case opDeleteNodegroup:
		h.deleteNodegroup(w, r, a.cluster, a.child)
	case opUpdateNodegroupConfig:
		h.updateNodegroupConfig(w, r, a.cluster, a.child)
	case opUpdateNodegroupVersion:
		h.updateNodegroupVersion(w, r, a.cluster, a.child)
	default:
		return false
	}

	return true
}

// serveFargateAddonOp runs op when it is a Fargate profile or add-on
// operation.
func (h *Handler) serveFargateAddonOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opCreateFargateProfile:
		h.createFargateProfile(w, r, a.cluster)
	case opListFargateProfiles:
		h.listFargateProfiles(w, r, a.cluster)
	case opDescribeFargateProfile:
		h.describeFargateProfile(w, r, a.cluster, a.child)
	case opDeleteFargateProfile:
		h.deleteFargateProfile(w, r, a.cluster, a.child)
	case opCreateAddon:
		h.createAddon(w, r, a.cluster)
	case opListAddons:
		h.listAddons(w, r, a.cluster)
	case opDescribeAddon:
		h.describeAddon(w, r, a.cluster, a.child)
	case opDeleteAddon:
		h.deleteAddon(w, r, a.cluster, a.child)
	case opUpdateAddon:
		h.updateAddon(w, r, a.cluster, a.child)
	default:
		return false
	}

	return true
}

// serveAccessOp runs op when it is an access entry operation.
func (h *Handler) serveAccessOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opCreateAccessEntry:
		h.createAccessEntry(w, r, a.cluster)
	case opListAccessEntries:
		h.listAccessEntries(w, r, a.cluster)
	case opDescribeAccessEntry:
		h.describeAccessEntry(w, r, a.cluster, a.child)
	case opUpdateAccessEntry:
		h.updateAccessEntry(w, r, a.cluster, a.child)
	case opDeleteAccessEntry:
		h.deleteAccessEntry(w, r, a.cluster, a.child)
	case opAssociateAccessPolicy:
		h.associateAccessPolicy(w, r, a.cluster, a.child)
	case opListAssociatedAccessPolicies:
		h.listAssociatedAccessPolicies(w, r, a.cluster, a.child)
	case opDisassociateAccessPolicy:
		h.disassociateAccessPolicy(w, r, a.cluster, a.child, a.policyARN)
	default:
		return false
	}

	return true
}

// serveAccountOp runs op when it is a tagging or account-level operation.
func (h *Handler) serveAccountOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opListAccessPolicies:
		h.listAccessPolicies(w, r)
	case opDescribeAddonVersions:
		h.describeAddonVersions(w, r)
	case opDescribeAddonConfiguration:
		h.describeAddonConfiguration(w, r)
	case opTagResource, opUntagResource, opListTagsForResource:
		h.serveTags(w, r, op, a)
	default:
		return false
	}

	return true
}

// writeUnknown writes the error of a request classify cannot name. None of
// these branches reads or changes state.
func (h *Handler) writeUnknown(w http.ResponseWriter, a *opArgs) {
	switch a.fail {
	case failNotFound:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", a.failMsg)
	case failMalformed:
		writeError(w, http.StatusBadRequest, "InvalidParameterException", a.failMsg)
	case failTagsMethod:
		if _, ok := h.eks.(clusterTagger); !ok {
			writeError(w, http.StatusNotImplemented, "InvalidRequestException", "tagging not supported")
			return
		}

		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	case failMethod:
		methodNotAllowed(w)
	}
}

// splitPath strips the /clusters prefix from an escaped path and splits the
// rest. Each segment is percent-decoded after the split, so a principal ARN
// sent as role%2Fname stays one segment. The slice is empty for the bare
// /clusters URL. ok is false when a segment has a bad escape.
func splitPath(escaped string) (parts []string, ok bool) {
	rest := strings.TrimPrefix(escaped, pathPrefix)
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		return nil, true
	}

	parts = strings.Split(rest, "/")
	for i, seg := range parts {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			return nil, false
		}

		parts[i] = dec
	}

	return parts, true
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidParameterException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// writeJSON encodes v as the JSON response body. Real EKS only ever returns
// 200 on success (errors go through writeError), so the status is fixed.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return serviceName }

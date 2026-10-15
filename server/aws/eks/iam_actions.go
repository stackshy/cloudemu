package eks

import (
	"net/http"
	"strconv"
	"strings"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Condition keys this handler sets (Service Authorization Reference, Amazon
// EKS, "Condition keys").
const (
	condKubernetesVersion  = "eks:kubernetesVersion"
	condEndpointPublic     = "eks:endpointPublicAccess"
	condEndpointPrivate    = "eks:endpointPrivateAccess"
	condAuthMode           = "eks:authenticationMode"
	condBootstrapAdmin     = "eks:bootstrapClusterCreatorAdminPermissions"
	condLoggingTypePrefix  = "eks:loggingType/"
	condClusterName        = "eks:clusterName"
	condPrincipalARN       = "eks:principalArn"
	condKubernetesGroups   = "eks:kubernetesGroups"
	condUsername           = "eks:username"
	condAccessEntryType    = "eks:accessEntryType"
	condPolicyARN          = "eks:policyArn"
	condAccessScope        = "eks:accessScope"
	condNamespaces         = "eks:namespaces"
	condTagKeys            = "aws:TagKeys"
	condRequestTagPrefix   = "aws:RequestTag/"
	condResourceTagPrefix  = "aws:ResourceTag/"
	actionTagResource      = serviceName + ":" + opTagResource
	missingChildIDWildcard = "*"
)

// WriteAccessDenied writes the 403 EKS returns when IAM denies a call: an
// AccessDeniedException in the restJson1 error shape.
func (*Handler) WriteAccessDenied(w http.ResponseWriter, _ *http.Request, msg string) {
	writeError(w, http.StatusForbidden, "AccessDeniedException", msg)
}

// IAMChecks names the IAM actions and resource ARNs a request needs.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, _, ok := h.IAMChecksWithContext(r, s)

	return checks, ok
}

// IAMChecksWithContext names the IAM actions and resource ARNs a request
// needs, and the EKS condition keys known from it. It takes the operation and
// names from classify, the same function ServeHTTP dispatches on, so the
// authorized operation and resource are the ones that run. ok=false is
// returned only for requests the handler answers with an error and no side
// effect.
//
// Actions are eks:<Operation>. Resources follow the Service Authorization
// Reference for Amazon EKS: ListClusters, CreateCluster, ListAccessPolicies
// and the add-on version lookups take no resource; creating or listing a
// child resource acts on the cluster; a nodegroup, Fargate profile, add-on or
// access entry operation acts on that resource's ARN. ARNs are built from the
// server's partition, region and account and the names dispatch uses.
func (h *Handler) IAMChecksWithContext(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, map[string]string, bool) {
	op, a := classify(r)

	rule, ok := iamRules[op]
	if !ok {
		return nil, nil, false
	}

	if s.Partition == "" {
		s.Partition = "aws"
	}

	c := &checkSet{h: h, r: r, scope: s, args: &a, cond: map[string]string{}}
	if !rule(c, op) {
		return nil, nil, false
	}

	return c.checks, c.cond, true
}

// checkSet collects the checks and condition keys of one request.
type checkSet struct {
	h      *Handler
	r      *http.Request
	scope  awsauthz.Scope
	args   *opArgs
	checks []awsauthz.Check
	cond   map[string]string
}

func (c *checkSet) add(action, resource string) {
	c.checks = append(c.checks, awsauthz.Check{Action: action, Resource: resource, Mode: awsauthz.Required})
}

func (c *checkSet) clusterARN() string {
	return c.scope.ARN(serviceName, arnKindCluster+"/"+c.args.cluster)
}

// childARN is the ARN of the nodegroup, Fargate profile or add-on name of the
// request's cluster, in the shape the provider gives them
// (<kind>/<cluster>/<name>).
func (c *checkSet) childARN(kind, name string) string {
	return c.scope.ARN(serviceName, kind+"/"+c.args.cluster+"/"+name)
}

func (c *checkSet) set(key, value string) {
	if value != "" {
		c.cond[key] = value
	}
}

func (c *checkSet) setBool(key string, v *bool) {
	if v != nil {
		c.cond[key] = strconv.FormatBool(*v)
	}
}

func (c *checkSet) setList(key string, values []string) {
	if len(values) > 0 {
		c.cond[key] = strings.Join(values, iamdriver.ConditionValueSeparator)
	}
}

// requestTags sets aws:RequestTag/* and aws:TagKeys from tags.
func (c *checkSet) requestTags(tags map[string]string) {
	keys := make([]string, 0, len(tags))
	for k, v := range tags {
		keys = append(keys, k)
		c.cond[condRequestTagPrefix+k] = v
	}

	c.setList(condTagKeys, keys)
}

// resourceTags sets aws:ResourceTag/* from tags.
func (c *checkSet) resourceTags(tags map[string]string) {
	for k, v := range tags {
		c.cond[condResourceTagPrefix+k] = v
	}
}

// clusterTags sets aws:ResourceTag/* from the request's cluster.
func (c *checkSet) clusterTags() {
	if cl, err := c.h.eks.DescribeCluster(c.r.Context(), c.args.cluster); err == nil {
		c.resourceTags(cl.Tags)
	}
}

// logging sets eks:loggingType/<type> for every log type the request turns
// on or off.
func (c *checkSet) logging(l *loggingJSON) {
	if l == nil {
		return
	}

	for _, setup := range l.ClusterLogging {
		for _, t := range setup.Types {
			c.cond[condLoggingTypePrefix+t] = strconv.FormatBool(setup.Enabled)
		}
	}
}

func (c *checkSet) vpc(v *vpcConfigRequest) {
	if v != nil {
		c.setBool(condEndpointPublic, v.EndpointPublicAccess)
		c.setBool(condEndpointPrivate, v.EndpointPrivateAccess)
	}
}

// iamRule adds the checks of one operation. It returns false when the
// handler will reject the request without side effects.
type iamRule func(c *checkSet, op opID) bool

func action(op opID) string { return serviceName + ":" + op }

// onAny: the operation's own action, which takes no resource.
func onAny(c *checkSet, op opID) bool {
	c.add(action(op), "*")
	return true
}

// onCluster: the operation's own action on the cluster.
func onCluster(c *checkSet, op opID) bool {
	c.add(action(op), c.clusterARN())
	c.clusterTags()

	return true
}

// onChild returns the rule of an operation on a nodegroup, Fargate profile or
// add-on of kind, named by the request.
func onChild(kind string) iamRule {
	return func(c *checkSet, op opID) bool {
		c.add(action(op), c.childARN(kind, c.args.child))
		c.resourceTags(c.h.childTags(c.r, kind, c.args.cluster, c.args.child))

		return true
	}
}

// createOnCluster returns the rule of a Create* operation whose body is a T
// with tags: the operation's own action on the cluster, with the request
// tags.
func createOnCluster[T any](tags func(*T) map[string]string) iamRule {
	return func(c *checkSet, op opID) bool {
		var body T
		if !awsauthz.JSONBody(c.r, &body) {
			return false
		}

		onCluster(c, op)
		c.requestTags(tags(&body))

		return true
	}
}

// iamRules maps every operation the handler runs to its checks. Operations
// absent here are the error paths, reported as unknown.
//
//nolint:gochecknoglobals // static lookup table
var iamRules = map[opID]iamRule{
	opListAccessPolicies:         onAny,
	opDescribeAddonVersions:      onAny,
	opDescribeAddonConfiguration: onAny,
	opListClusters:               onAny,
	opCreateCluster:              createClusterChecks,

	opDescribeCluster:      onCluster,
	opDeleteCluster:        onCluster,
	opUpdateClusterConfig:  updateClusterConfigChecks,
	opUpdateClusterVersion: updateClusterVersionChecks,
	opListUpdates:          updatesChecks,
	opDescribeUpdate:       updatesChecks,

	opCreateNodegroup:        createOnCluster(func(b *createNodegroupRequest) map[string]string { return b.Tags }),
	opListNodegroups:         onCluster,
	opDescribeNodegroup:      onChild(arnKindNodegroup),
	opDeleteNodegroup:        onChild(arnKindNodegroup),
	opUpdateNodegroupConfig:  onChild(arnKindNodegroup),
	opUpdateNodegroupVersion: onChild(arnKindNodegroup),

	opCreateFargateProfile:   createOnCluster(func(b *createFargateProfileRequest) map[string]string { return b.Tags }),
	opListFargateProfiles:    onCluster,
	opDescribeFargateProfile: onChild(arnKindFargate),
	opDeleteFargateProfile:   onChild(arnKindFargate),

	opCreateAddon:   createOnCluster(func(b *createAddonRequest) map[string]string { return b.Tags }),
	opListAddons:    onCluster,
	opDescribeAddon: onChild(arnKindAddon),
	opDeleteAddon:   onChild(arnKindAddon),
	opUpdateAddon:   onChild(arnKindAddon),

	opCreateAccessEntry:            createAccessEntryChecks,
	opListAccessEntries:            onCluster,
	opDescribeAccessEntry:          accessEntryChecks,
	opUpdateAccessEntry:            accessEntryChecks,
	opDeleteAccessEntry:            accessEntryChecks,
	opAssociateAccessPolicy:        associatePolicyChecks,
	opListAssociatedAccessPolicies: accessEntryChecks,
	opDisassociateAccessPolicy:     disassociatePolicyChecks,

	opTagResource:         tagChecks,
	opUntagResource:       tagChecks,
	opListTagsForResource: tagChecks,
}

// createClusterChecks: eks:CreateCluster, which takes no resource, with the
// cluster settings the request carries as condition keys.
func createClusterChecks(c *checkSet, op opID) bool {
	var body createClusterRequest
	if !awsauthz.JSONBody(c.r, &body) {
		return false
	}

	c.add(action(op), "*")
	c.set(condKubernetesVersion, body.Version)
	c.vpc(body.ResourcesVpcConfig)
	c.logging(body.Logging)

	if ac := body.AccessConfig; ac != nil {
		c.set(condAuthMode, ac.AuthenticationMode)
		c.setBool(condBootstrapAdmin, ac.BootstrapClusterCreatorAdminPermissions)
	}

	c.requestTags(body.Tags)

	return true
}

// updateClusterConfigChecks: eks:UpdateClusterConfig on the cluster with the
// settings it changes; tags it sets also need eks:TagResource.
func updateClusterConfigChecks(c *checkSet, op opID) bool {
	var body updateClusterConfigRequest
	if !awsauthz.JSONBody(c.r, &body) {
		return false
	}

	onCluster(c, op)
	c.vpc(body.ResourcesVpcConfig)
	c.logging(body.Logging)

	if body.AccessConfig != nil {
		c.set(condAuthMode, body.AccessConfig.AuthenticationMode)
	}

	if len(body.Tags) > 0 {
		c.add(actionTagResource, c.clusterARN())
		c.requestTags(body.Tags)
	}

	return true
}

// updateClusterVersionChecks: eks:UpdateClusterVersion on the cluster, with
// eks:kubernetesVersion.
func updateClusterVersionChecks(c *checkSet, op opID) bool {
	var body updateClusterVersionRequest
	if !awsauthz.JSONBody(c.r, &body) {
		return false
	}

	onCluster(c, op)
	c.set(condKubernetesVersion, body.Version)

	return true
}

// updatesChecks: ListUpdates and DescribeUpdate act on the cluster, or on the
// nodegroup or add-on the request names (nodegroupName, addonName), which is
// the resource whose updates dispatch reads.
func updatesChecks(c *checkSet, op opID) bool {
	q := c.r.URL.Query()
	ng, addon := q.Get("nodegroupName"), q.Get("addonName")

	if ng == "" && addon == "" {
		return onCluster(c, op)
	}

	if ng != "" {
		c.add(action(op), c.childARN(arnKindNodegroup, ng))
		c.resourceTags(c.h.childTags(c.r, arnKindNodegroup, c.args.cluster, ng))
	}

	if addon != "" {
		c.add(action(op), c.childARN(arnKindAddon, addon))
		c.resourceTags(c.h.childTags(c.r, arnKindAddon, c.args.cluster, addon))
	}

	return true
}

// createAccessEntryChecks: eks:CreateAccessEntry on the cluster, with the
// entry's principal, type, username and groups.
func createAccessEntryChecks(c *checkSet, op opID) bool {
	var body createAccessEntryRequest
	if !awsauthz.JSONBody(c.r, &body) {
		return false
	}

	onCluster(c, op)
	c.set(condPrincipalARN, body.PrincipalArn)
	c.set(condAccessEntryType, body.Type)
	c.set(condUsername, body.Username)
	c.setList(condKubernetesGroups, body.KubernetesGroups)
	c.requestTags(body.Tags)

	return true
}

// accessEntry adds the check of op on the access entry the request names and
// sets the access entry condition keys from it. The entry's ARN ends in an id
// EKS assigns, so an entry that does not exist is checked on
// access-entry/<cluster>/*: a grant on the cluster's entries covers it, and
// the operation then answers ResourceNotFoundException.
func (c *checkSet) accessEntry(op opID) *eksdriver.AccessEntry {
	c.set(condClusterName, c.args.cluster)
	c.set(condPrincipalARN, c.args.child)

	e, err := c.h.eks.DescribeAccessEntry(c.r.Context(), c.args.cluster, c.args.child)
	if err != nil {
		c.add(action(op), c.scope.ARN(serviceName, arnKindAccessEntry+"/"+c.args.cluster+"/"+missingChildIDWildcard))
		return nil
	}

	c.add(action(op), e.ARN)
	c.set(condAccessEntryType, e.Type)
	c.set(condUsername, e.Username)
	c.setList(condKubernetesGroups, e.KubernetesGroups)
	c.resourceTags(e.Tags)

	return e
}

func accessEntryChecks(c *checkSet, op opID) bool {
	c.accessEntry(op)
	return true
}

// associatePolicyChecks: eks:AssociateAccessPolicy on the access entry, with
// the policy and access scope the request names.
func associatePolicyChecks(c *checkSet, op opID) bool {
	var body associateAccessPolicyRequest
	if !awsauthz.JSONBody(c.r, &body) {
		return false
	}

	c.accessEntry(op)
	c.set(condPolicyARN, body.PolicyArn)
	c.set(condAccessScope, body.AccessScope.Type)
	c.setList(condNamespaces, body.AccessScope.Namespaces)

	return true
}

// disassociatePolicyChecks: eks:DisassociateAccessPolicy on the access entry,
// with the policy the path names and the access scope it is associated with.
func disassociatePolicyChecks(c *checkSet, op opID) bool {
	e := c.accessEntry(op)
	c.set(condPolicyARN, c.args.policyARN)

	if e == nil {
		return true
	}

	for _, p := range e.Policies {
		if p.PolicyArn == c.args.policyARN {
			c.set(condAccessScope, p.AccessScope.Type)
			c.setList(condNamespaces, p.AccessScope.Namespaces)
		}
	}

	return true
}

// tagChecks: the tagging action on the resource the tagging ARN names, rebuilt
// from the names the provider resolves it by. An ARN of another account or
// region, or one that is not an EKS resource ARN, names nothing here and
// dispatch answers it with an error.
func tagChecks(c *checkSet, op opID) bool {
	t := &c.args.tag
	if t.foreign(c.scope.AccountID, c.scope.Region) {
		return false
	}

	c.args.cluster = t.cluster

	resource := c.args.tagARN

	switch t.kind {
	case arnKindCluster:
		resource = c.clusterARN()
	case arnKindNodegroup, arnKindFargate, arnKindAddon:
		resource = c.childARN(t.kind, t.name)
	}

	switch op {
	case opTagResource:
		var body struct {
			Tags map[string]string `json:"tags"`
		}

		if !awsauthz.JSONBody(c.r, &body) {
			return false
		}

		c.requestTags(body.Tags)
	case opUntagResource:
		c.setList(condTagKeys, c.r.URL.Query()["tagKeys"])
	}

	c.add(action(op), resource)

	if tagger, ok := c.h.eks.(clusterTagger); ok {
		if tags, err := tagger.ListResourceTags(c.r.Context(), resource); err == nil {
			c.resourceTags(tags)
		}
	}

	return true
}

// childTags returns the tags of the nodegroup, Fargate profile or add-on name
// of cluster, or nil when it does not exist.
func (h *Handler) childTags(r *http.Request, kind, cluster, name string) map[string]string {
	ctx := r.Context()

	switch kind {
	case arnKindNodegroup:
		if ng, err := h.eks.DescribeNodegroup(ctx, cluster, name); err == nil {
			return ng.Tags
		}
	case arnKindFargate:
		if fp, err := h.eks.DescribeFargateProfile(ctx, cluster, name); err == nil {
			return fp.Tags
		}
	case arnKindAddon:
		if ad, err := h.eks.DescribeAddon(ctx, cluster, name); err == nil {
			return ad.Tags
		}
	}

	return nil
}

package cloudfront

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Global condition keys this handler sets (Service Authorization Reference,
// Amazon CloudFront).
const (
	condTagKeys           = "aws:TagKeys"
	condRequestTagPrefix  = "aws:RequestTag/"
	condResourceTagPrefix = "aws:ResourceTag/"
)

// The IAM actions whose name differs from the operation.
const (
	actionCreateDistribution = serviceName + ":" + opCreateDistribution
	actionTagResource        = serviceName + ":" + opTagResource
)

// IAMChecks names the IAM actions and resource ARNs a request needs.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, _, ok := h.IAMChecksWithContext(r, s)

	return checks, ok
}

// IAMChecksWithContext names the IAM actions and resource ARNs a request
// needs, and the tag condition keys known from it. It takes the operation and
// distribution id from classify, the same function ServeHTTP dispatches on,
// so the authorized operation and resource are the ones that run. ok=false
// is returned only for requests the handler answers with an error and no
// side effect.
//
// CloudFront is global: a distribution ARN has an account and no region
// (arn:aws:cloudfront::123456789012:distribution/E1, Service Authorization
// Reference, Amazon CloudFront). CreateDistribution and ListDistributions take
// no resource ("*"). CreateDistributionWithTags is authorized as
// cloudfront:CreateDistribution plus cloudfront:TagResource (CloudFront API
// Reference, CreateDistributionWithTags).
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

// distributionARN is the ARN of the distribution the request names, in this
// account.
func (c *checkSet) distributionARN() string {
	return c.scope.GlobalARN(serviceName, arnDistributionPrefix+c.args.id)
}

// requestTags sets aws:RequestTag/* and aws:TagKeys from tags.
func (c *checkSet) requestTags(tags map[string]string) {
	keys := make([]string, 0, len(tags))
	for k, v := range tags {
		keys = append(keys, k)
		c.cond[condRequestTagPrefix+k] = v
	}

	c.tagKeys(keys)
}

func (c *checkSet) tagKeys(keys []string) {
	if len(keys) > 0 {
		c.cond[condTagKeys] = strings.Join(keys, iamdriver.ConditionValueSeparator)
	}
}

// resourceTags sets aws:ResourceTag/* from the stored tags of the
// distribution the request names.
func (c *checkSet) resourceTags() {
	dist, err := c.h.cf.GetDistribution(c.r.Context(), c.args.id)
	if err != nil {
		return
	}

	for k, v := range dist.Tags {
		c.cond[condResourceTagPrefix+k] = v
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

// onDistribution: the operation's own action on the distribution.
func onDistribution(c *checkSet, op opID) bool {
	c.add(action(op), c.distributionARN())
	c.resourceTags()

	return true
}

// iamRules maps every operation the handler runs to its checks. Operations
// absent here are the error paths, reported as unknown.
//
//nolint:gochecknoglobals // static lookup table
var iamRules = map[opID]iamRule{
	opCreateDistribution:         onAny,
	opCreateDistributionWithTags: createWithTagsChecks,
	opListDistributions:          onAny,
	opGetDistribution:            onDistribution,
	opDeleteDistribution:         onDistribution,
	opGetDistributionConfig:      onDistribution,
	opUpdateDistribution:         onDistribution,
	opCreateInvalidation:         onDistribution,
	opListInvalidations:          onDistribution,
	opGetInvalidation:            onDistribution,
	opListTagsForResource:        taggingChecks,
	opTagResource:                taggingChecks,
	opUntagResource:              taggingChecks,
}

// createWithTagsChecks: cloudfront:CreateDistribution on "*" and
// cloudfront:TagResource on the distribution being created. Its id is not
// known yet, so TagResource is checked on the ARN with a literal "*" id: a
// grant on distribution/* (or "*") covers it, and a deny on distribution/*
// applies, while a statement naming one existing distribution cannot match a
// distribution that does not exist yet.
func createWithTagsChecks(c *checkSet, _ opID) bool {
	var req distributionConfigWithTagsRequest
	if !awsauthz.XMLBody(c.r, &req) {
		return false
	}

	c.add(actionCreateDistribution, "*")
	c.add(actionTagResource, c.scope.GlobalARN(serviceName, arnDistributionPrefix+"*"))
	c.requestTags(req.Tags.toMap())

	return true
}

// taggingChecks: the tagging action on the distribution the Resource ARN
// names. An ARN that is not a distribution ARN of this account names nothing
// here; dispatch answers it with NoSuchResource.
func taggingChecks(c *checkSet, op opID) bool {
	if c.args.id == "" || c.args.resourceAccount != c.scope.AccountID {
		return false
	}

	switch op {
	case opTagResource:
		var req tagsXML
		if !awsauthz.XMLBody(c.r, &req) {
			return false
		}

		c.requestTags(req.toMap())
	case opUntagResource:
		var req tagKeysRequest
		if !awsauthz.XMLBody(c.r, &req) {
			return false
		}

		c.tagKeys(req.Items)
	}

	return onDistribution(c, op)
}

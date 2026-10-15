package route53

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// serviceName is the IAM service prefix of Route 53 actions and the service
// field of Route 53 ARNs.
const serviceName = "route53"

// Condition keys this handler sets (Route 53 Developer Guide, "Using IAM
// policy conditions for fine-grained access control").
const (
	condRecordActions = "route53:ChangeResourceRecordSetsActions"
	condRecordTypes   = "route53:ChangeResourceRecordSetsRecordTypes"
	condRecordNames   = "route53:ChangeResourceRecordSetsNormalizedRecordNames"
	condVPCs          = "route53:VPCs"
)

// actionDescribeVpcs is the EC2 permission Route 53 needs to read the VPC a
// private hosted zone is created with or associated to (Route 53 API
// Reference, CreateHostedZone). ec2:DescribeVpcs takes no resource.
const actionDescribeVpcs = "ec2:DescribeVpcs"

// IAMChecks names the IAM actions and resource ARNs a request needs.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, _, ok := h.IAMChecksWithContext(r, s)

	return checks, ok
}

// IAMChecksWithContext names the IAM actions and resource ARNs a request
// needs, and the Route 53 condition keys known from it. It takes the
// operation and ids from classify, the same function ServeHTTP dispatches on,
// so the authorized operation and resource are the ones that run. ok=false
// is returned only for requests the handler answers with an error and no
// side effect.
//
// Route 53 is global: hosted zone, health check and change ARNs carry neither
// region nor account (arn:aws:route53:::hostedzone/Z1, Service Authorization
// Reference, Amazon Route 53).
func (*Handler) IAMChecksWithContext(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, map[string]string, bool) {
	op, a := classify(r)

	rule, ok := iamRules[op]
	if !ok {
		return nil, nil, false
	}

	if s.Partition == "" {
		s.Partition = "aws"
	}

	c := &checkSet{r: r, scope: s, args: &a, cond: map[string]string{}}
	if !rule(c, op) {
		return nil, nil, false
	}

	return c.checks, c.cond, true
}

// checkSet collects the checks and condition keys of one request.
type checkSet struct {
	r      *http.Request
	scope  awsauthz.Scope
	args   *opArgs
	checks []awsauthz.Check
	cond   map[string]string
}

func (c *checkSet) add(action, resource string) {
	c.checks = append(c.checks, awsauthz.Check{Action: action, Resource: resource, Mode: awsauthz.Required})
}

func (c *checkSet) arn(kind string) string {
	return c.scope.PartitionARN(serviceName, kind+"/"+c.args.id)
}

// vpc sets route53:VPCs ("VPCId=<vpc-id>,VPCRegion=<region>", lower case)
// for the VPC a request names.
func (c *checkSet) vpc(vpcID, region string) {
	c.cond[condVPCs] = "VPCId=" + strings.ToLower(vpcID) + ",VPCRegion=" + strings.ToLower(region)
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

// onZone: the operation's own action on the hosted zone.
func onZone(c *checkSet, op opID) bool {
	c.add(action(op), c.arn(tagTypeHostedZone))
	return true
}

// onChange: the operation's own action on the change.
func onChange(c *checkSet, op opID) bool {
	c.add(action(op), c.arn("change"))
	return true
}

// onHealthCheck: the operation's own action on the health check.
func onHealthCheck(c *checkSet, op opID) bool {
	c.add(action(op), c.arn(tagTypeHealthCheck))
	return true
}

// iamRules maps every operation the handler runs to its checks. Operations
// absent here are the error paths, reported as unknown.
//
//nolint:gochecknoglobals // static lookup table
var iamRules = map[opID]iamRule{
	opCreateHostedZone:              createHostedZoneChecks,
	opListHostedZones:               onAny,
	opGetHostedZone:                 onZone,
	opDeleteHostedZone:              onZone,
	opUpdateHostedZoneComment:       onZone,
	opChangeResourceRecordSets:      changeRecordSetsChecks,
	opListResourceRecordSets:        onZone,
	opAssociateVPCWithHostedZone:    associateVPCChecks,
	opDisassociateVPCFromHostedZone: disassociateVPCChecks,
	opGetChange:                     onChange,
	opGetHostedZoneCount:            onAny,
	opListHostedZonesByName:         onAny,
	opListHostedZonesByVPC:          listByVPCChecks,
	opTestDNSAnswer:                 onAny,
	opCreateHealthCheck:             onAny,
	opListHealthChecks:              onAny,
	opGetHealthCheck:                onHealthCheck,
	opUpdateHealthCheck:             onHealthCheck,
	opDeleteHealthCheck:             onHealthCheck,
	opChangeTagsForResource:         tagChecks,
	opListTagsForResource:           tagChecks,
}

// createHostedZoneChecks: route53:CreateHostedZone, plus ec2:DescribeVpcs
// and route53:VPCs for a private zone created with a VPC.
func createHostedZoneChecks(c *checkSet, op opID) bool {
	var req createHostedZoneRequest
	if !awsauthz.XMLBody(c.r, &req) {
		return false
	}

	c.add(action(op), "*")

	if req.VPC != nil {
		c.vpc(req.VPC.VPCId, req.VPC.VPCRegion)
		c.add(actionDescribeVpcs, "*")
	}

	return true
}

// associateVPCChecks: AssociateVPCWithHostedZone on the zone, ec2:DescribeVpcs
// to read the VPC, and route53:VPCs.
func associateVPCChecks(c *checkSet, op opID) bool {
	var req associateVPCRequest
	if !awsauthz.XMLBody(c.r, &req) {
		return false
	}

	c.add(action(op), c.arn(tagTypeHostedZone))
	c.add(actionDescribeVpcs, "*")
	c.vpc(req.VPC.VPCId, req.VPC.VPCRegion)

	return true
}

// disassociateVPCChecks: DisassociateVPCFromHostedZone on the zone and
// route53:VPCs.
func disassociateVPCChecks(c *checkSet, op opID) bool {
	var req disassociateVPCRequest
	if !awsauthz.XMLBody(c.r, &req) {
		return false
	}

	c.add(action(op), c.arn(tagTypeHostedZone))
	c.vpc(req.VPC.VPCId, req.VPC.VPCRegion)

	return true
}

// listByVPCChecks: ListHostedZonesByVPC, with route53:VPCs from the vpcid and
// vpcregion query parameters dispatch reads.
func listByVPCChecks(c *checkSet, op opID) bool {
	c.add(action(op), "*")

	q := c.r.URL.Query()
	if id, region := q.Get("vpcid"), q.Get("vpcregion"); id != "" && region != "" {
		c.vpc(id, region)
	}

	return true
}

// tagChecks: the tagging action on the hosted zone or health check the
// request names. classify only names these operations for those two types.
func tagChecks(c *checkSet, op opID) bool {
	c.add(action(op), c.arn(c.args.tagType))
	return true
}

// changeRecordSetsChecks: ChangeResourceRecordSets on the zone, with the
// actions, record types and normalized record names of every change in the
// batch, decoded from the body the same way dispatch decodes it.
func changeRecordSetsChecks(c *checkSet, op opID) bool {
	var req changeResourceRecordSetsRequest
	if !awsauthz.XMLBody(c.r, &req) {
		return false
	}

	c.add(action(op), c.arn(tagTypeHostedZone))

	var actions, types, names []string

	for i := range req.ChangeBatch.Changes {
		ch := &req.ChangeBatch.Changes[i]
		actions = appendUnique(actions, ch.Action)
		types = appendUnique(types, strings.ToUpper(ch.ResourceRecordSet.Type))
		names = appendUnique(names, normalizeRecordName(ch.ResourceRecordSet.Name))
	}

	c.setList(condRecordActions, actions)
	c.setList(condRecordTypes, types)
	c.setList(condRecordNames, names)

	return true
}

func (c *checkSet) setList(key string, values []string) {
	if len(values) > 0 {
		c.cond[key] = strings.Join(values, iamdriver.ConditionValueSeparator)
	}
}

func appendUnique(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}

	return append(list, v)
}

// normalizeRecordName returns a record name in the form the
// route53:ChangeResourceRecordSetsNormalizedRecordNames condition key uses:
// lower case, no trailing dot, and every character other than a-z, 0-9, '-',
// '_' and '.' written as a backslash and its three-digit octal code (so '*'
// is \052). An escape already in that form is kept, so "*.example.com" and
// "\052.example.com" normalize alike.
func normalizeRecordName(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".")

	var b strings.Builder

	for i := 0; i < len(name); i++ {
		ch := name[i]

		switch {
		case isOctalEscape(name[i:]):
			b.WriteString(name[i : i+octalEscapeLen])
			i += octalEscapeLen - 1
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-', ch == '_', ch == '.':
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "\\%03o", ch)
		}
	}

	return b.String()
}

// octalEscapeLen is the length of a \ooo escape.
const octalEscapeLen = 4

// isOctalEscape reports whether s starts with a \ooo escape.
func isOctalEscape(s string) bool {
	if len(s) < octalEscapeLen || s[0] != '\\' {
		return false
	}

	for _, d := range s[1:octalEscapeLen] {
		if d < '0' || d > '7' {
			return false
		}
	}

	return true
}

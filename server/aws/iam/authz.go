package iam

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// entityKind is the IAM resource type an operation is authorized on.
type entityKind int

const (
	kindAccount         entityKind = iota // no resource, Resource "*"
	kindUser                              // UserName
	kindRole                              // RoleName
	kindGroup                             // GroupName
	kindInstanceProfile                   // InstanceProfileName
	kindPolicyName                        // PolicyName and Path (CreatePolicy)
	kindPolicyARN                         // PolicyArn
	kindMFAName                           // VirtualMFADeviceName (CreateVirtualMFADevice)
	kindMFASerial                         // SerialNumber (DeleteVirtualMFADevice)
	kindPolicySource                      // PolicySourceArn (SimulatePrincipalPolicy)
	kindUnnamed                           // the request does not name the resource (CreateServiceLinkedRole)
)

// entityType is the ARN resource type and name parameter of each named kind.
//
//nolint:gochecknoglobals // static lookup table
var entityType = map[entityKind]struct{ arnType, param string }{
	kindUser:            {"user", "UserName"},
	kindRole:            {"role", "RoleName"},
	kindGroup:           {"group", "GroupName"},
	kindInstanceProfile: {"instance-profile", "InstanceProfileName"},
	kindPolicyName:      {"policy", "PolicyName"},
	kindMFAName:         {"mfa", "VirtualMFADeviceName"},
}

var (
	// entityName is the shape of an IAM user, role, group, policy, instance
	// profile or MFA device name.
	entityName = regexp.MustCompile(`^[\w+=,.@-]{1,128}$`)
	// entityPath is the shape of an IAM path: "/" or "/segments/".
	entityPath = regexp.MustCompile(`^/([\x21-\x7E]{0,510}/)?$`)
)

// IAMChecks names the IAM action of a request from the form Action that
// ServeHTTP dispatches on, and the user, role, group, policy, instance
// profile or MFA device it acts on. An entity that exists is named by its
// stored ARN, path included; otherwise the ARN is built from the request's
// Path and name, the way the backend builds it on create. The deny message
// names only what the request sent. List and account operations take
// Resource "*", and a request that names no well-formed resource is
// evaluated on an unknown one. An Action the handler does not know is
// authorized as such and then answered with InvalidAction, so nothing runs.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, ok := awsauthz.QueryChecks(r, h.IAMService())
	if !ok {
		return nil, false
	}

	kind, known := iamActions[r.Form.Get("Action")]
	if !known {
		return checks, true
	}

	checks[0].Resource, checks[0].MessageResource = h.entityResource(r, kind, s)

	return checks, true
}

// entityResource is the resource a request acts on and, when it differs, the
// resource its deny message names.
func (h *Handler) entityResource(r *http.Request, kind entityKind, s awsauthz.Scope) (resource, message string) {
	switch kind {
	case kindAccount:
		return "*", ""
	case kindPolicyARN:
		return h.accountARN(r.Form.Get("PolicyArn"), s, "policy/"), ""
	case kindMFASerial:
		return h.accountARN(r.Form.Get("SerialNumber"), s, "mfa/"), ""
	case kindPolicySource:
		return h.accountARN(r.Form.Get("PolicySourceArn"), s, "user/", "role/", "group/"), ""
	case kindUser, kindRole, kindGroup, kindInstanceProfile, kindPolicyName, kindMFAName:
		return h.namedEntity(r, kind, s)
	case kindUnnamed:
	}

	return "", ""
}

// namedEntity resolves an entity named by a name parameter and optional Path.
func (h *Handler) namedEntity(r *http.Request, kind entityKind, s awsauthz.Scope) (resource, message string) {
	t := entityType[kind]
	name := r.Form.Get(t.param)

	if !entityName.MatchString(name) {
		return "", ""
	}

	// The backend files an MFA device by name alone, whatever its Path.
	path := "/"
	if p := r.Form.Get("Path"); p != "" && kind != kindMFAName {
		path = p
	}

	if !entityPath.MatchString(path) {
		return "", ""
	}

	built := s.GlobalARN("iam", t.arnType+"/"+strings.TrimPrefix(path, "/")+name)

	stored := h.storedARN(r.Context(), kind, name)
	if stored == "" || stored == built {
		return built, ""
	}

	return stored, s.GlobalARN("iam", t.arnType+"/"+name)
}

// storedARN is the ARN of an existing user, role, group or instance profile,
// or "" when there is none.
func (h *Handler) storedARN(ctx context.Context, kind entityKind, name string) string {
	switch kind {
	case kindUser:
		if u, err := h.iam.GetUser(ctx, name); err == nil {
			return u.ARN
		}
	case kindRole:
		if ro, err := h.iam.GetRole(ctx, name); err == nil {
			return ro.ARN
		}
	case kindGroup:
		if g, err := h.iam.GetGroup(ctx, name); err == nil {
			return g.ARN
		}
	case kindInstanceProfile:
		if p, err := h.iam.GetInstanceProfile(ctx, name); err == nil {
			return p.ARN
		}
	case kindAccount, kindPolicyName, kindPolicyARN, kindMFAName, kindMFASerial, kindPolicySource, kindUnnamed:
	}

	return ""
}

// accountARN returns arn when it is an IAM ARN in the server's partition, of
// one of the given resource types, owned by the server's account (or, for a
// managed policy, by AWS). Anything else is "" (unknown): the handler looks
// these ARNs up exactly, so a foreign one names nothing here.
func (*Handler) accountARN(arn string, s awsauthz.Scope, types ...string) string {
	prefix := "arn:" + s.Partition + ":iam::"
	if !strings.HasPrefix(arn, prefix) {
		return ""
	}

	account, res, ok := strings.Cut(strings.TrimPrefix(arn, prefix), ":")
	if !ok || (account != s.AccountID && (account != "aws" || !strings.HasPrefix(res, "policy/"))) {
		return ""
	}

	for _, t := range types {
		if strings.HasPrefix(res, t) && len(res) > len(t) {
			return arn
		}
	}

	return ""
}

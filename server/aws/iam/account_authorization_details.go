package iam

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// GetAccountAuthorizationDetails returns a single aggregated snapshot of every
// user, group, role, and managed policy in the account, each with its inline and
// attached policies. It reads the same surfaces the individual list operations
// expose (ListUserPolicies, ListGroupPolicies, ListAttachedGroupPolicies, etc.),
// so it introduces no new provider state; what isn't modeled (RoleLastUsed,
// PermissionsBoundary) is emitted empty or omitted rather than fabricated.

type stringListXML struct {
	Member []string `xml:"member,omitempty"`
}

// policyDetailXML is an inline (embedded) policy: a name plus its JSON document.
type policyDetailXML struct {
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type policyDetailListXML struct {
	Member []policyDetailXML `xml:"member,omitempty"`
}

type userDetailXML struct {
	Path                    string              `xml:"Path,omitempty"`
	UserName                string              `xml:"UserName"`
	UserID                  string              `xml:"UserId"`
	Arn                     string              `xml:"Arn"`
	CreateDate              string              `xml:"CreateDate,omitempty"`
	GroupList               *stringListXML      `xml:"GroupList,omitempty"`
	AttachedManagedPolicies attachedPoliciesXML `xml:"AttachedManagedPolicies"`
	UserPolicyList          policyDetailListXML `xml:"UserPolicyList"`
	Tags                    *tagsXML            `xml:"Tags,omitempty"`
}

type groupDetailXML struct {
	Path                    string              `xml:"Path,omitempty"`
	GroupName               string              `xml:"GroupName"`
	GroupID                 string              `xml:"GroupId"`
	Arn                     string              `xml:"Arn"`
	CreateDate              string              `xml:"CreateDate,omitempty"`
	AttachedManagedPolicies attachedPoliciesXML `xml:"AttachedManagedPolicies"`
	GroupPolicyList         policyDetailListXML `xml:"GroupPolicyList"`
}

type roleDetailXML struct {
	Path                     string                  `xml:"Path,omitempty"`
	RoleName                 string                  `xml:"RoleName"`
	RoleID                   string                  `xml:"RoleId"`
	Arn                      string                  `xml:"Arn"`
	CreateDate               string                  `xml:"CreateDate,omitempty"`
	AssumeRolePolicyDocument string                  `xml:"AssumeRolePolicyDocument,omitempty"`
	InstanceProfileList      instanceProfilesListXML `xml:"InstanceProfileList"`
	RolePolicyList           policyDetailListXML     `xml:"RolePolicyList"`
	AttachedManagedPolicies  attachedPoliciesXML     `xml:"AttachedManagedPolicies"`
	Tags                     *tagsXML                `xml:"Tags,omitempty"`
}

type managedPolicyDetailXML struct {
	PolicyName       string `xml:"PolicyName"`
	PolicyID         string `xml:"PolicyId"`
	Arn              string `xml:"Arn"`
	Path             string `xml:"Path,omitempty"`
	DefaultVersionID string `xml:"DefaultVersionId,omitempty"`
	AttachmentCount  int    `xml:"AttachmentCount"`
	// PermissionsBoundaryUsageCount is the number of principals using this policy
	// as a permissions boundary. The emulator does not model permissions
	// boundaries, so it is always 0 — but real GAAD always emits the element.
	PermissionsBoundaryUsageCount int                   `xml:"PermissionsBoundaryUsageCount"`
	IsAttachable                  bool                  `xml:"IsAttachable"`
	CreateDate                    string                `xml:"CreateDate,omitempty"`
	UpdateDate                    string                `xml:"UpdateDate,omitempty"`
	PolicyVersionList             policyVersionsListXML `xml:"PolicyVersionList"`
}

type userDetailListXML struct {
	Member []userDetailXML `xml:"member,omitempty"`
}

type groupDetailListXML struct {
	Member []groupDetailXML `xml:"member,omitempty"`
}

type roleDetailListXML struct {
	Member []roleDetailXML `xml:"member,omitempty"`
}

type managedPolicyDetailListXML struct {
	Member []managedPolicyDetailXML `xml:"member,omitempty"`
}

type getAccountAuthorizationDetailsResult struct {
	UserDetailList  userDetailListXML          `xml:"UserDetailList"`
	GroupDetailList groupDetailListXML         `xml:"GroupDetailList"`
	RoleDetailList  roleDetailListXML          `xml:"RoleDetailList"`
	Policies        managedPolicyDetailListXML `xml:"Policies"`
	IsTruncated     bool                       `xml:"IsTruncated"`
	Marker          string                     `xml:"Marker,omitempty"`
}

type getAccountAuthorizationDetailsResponse struct {
	XMLName  xml.Name                             `xml:"GetAccountAuthorizationDetailsResponse"`
	Xmlns    string                               `xml:"xmlns,attr"`
	Result   getAccountAuthorizationDetailsResult `xml:"GetAccountAuthorizationDetailsResult"`
	Metadata responseMetadata                     `xml:"ResponseMetadata"`
}

// gaadFilter records which entity kinds the request's Filter.member.N selects.
// An absent filter selects everything (real IAM default).
type gaadFilter struct {
	users         bool
	groups        bool
	roles         bool
	localPolicies bool
	awsPolicies   bool
}

// parseGAADFilter maps Filter.member.N values to the selected entity kinds. It
// returns the first unrecognized value (if any) so the caller can answer with a
// ValidationError, matching real IAM which rejects an out-of-enum filter.
func parseGAADFilter(form url.Values) (gaadFilter, string) {
	indices := awsquery.CollectIndices(form, "Filter.member")
	if len(indices) == 0 {
		return gaadFilter{users: true, groups: true, roles: true, localPolicies: true, awsPolicies: true}, ""
	}

	var f gaadFilter

	for _, n := range indices {
		switch v := form.Get("Filter.member." + strconv.Itoa(n)); v {
		case "User":
			f.users = true
		case "Group":
			f.groups = true
		case "Role":
			f.roles = true
		case "LocalManagedPolicy":
			f.localPolicies = true
		case "AWSManagedPolicy":
			f.awsPolicies = true
		default:
			return f, v
		}
	}

	return f, ""
}

func (h *Handler) getAccountAuthorizationDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filter, invalid := parseGAADFilter(r.Form)
	if invalid != "" {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "ValidationError",
			"Value '"+invalid+"' at 'filter' failed to satisfy constraint: "+
				"Member must satisfy enum value set: "+
				"[User, Role, Group, LocalManagedPolicy, AWSManagedPolicy]")

		return
	}

	var users []userDetailXML
	if filter.users {
		users = h.buildUserDetails(ctx)
	}

	var groups []groupDetailXML
	if filter.groups {
		groups = h.buildGroupDetails(ctx)
	}

	var roles []roleDetailXML
	if filter.roles {
		roles = h.buildRoleDetails(ctx)
	}

	policies := h.buildPolicyDetails(ctx, filter)

	total := len(users) + len(groups) + len(roles) + len(policies)
	start, end, marker, truncated := pageWindow(total, r.Form)

	uLo, uHi := segWindow(len(users), 0, start, end)
	gOff := len(users)
	gLo, gHi := segWindow(len(groups), gOff, start, end)
	rOff := gOff + len(groups)
	rLo, rHi := segWindow(len(roles), rOff, start, end)
	pOff := rOff + len(roles)
	pLo, pHi := segWindow(len(policies), pOff, start, end)

	awsquery.WriteXMLResponse(w, getAccountAuthorizationDetailsResponse{
		Xmlns: Namespace,
		Result: getAccountAuthorizationDetailsResult{
			UserDetailList:  userDetailListXML{Member: users[uLo:uHi]},
			GroupDetailList: groupDetailListXML{Member: groups[gLo:gHi]},
			RoleDetailList:  roleDetailListXML{Member: roles[rLo:rHi]},
			Policies:        managedPolicyDetailListXML{Member: policies[pLo:pHi]},
			IsTruncated:     truncated,
			Marker:          marker,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// segWindow maps a global page window [winStart, winEnd) onto a single segment
// of the flattened [users, groups, roles, policies] sequence. segStart is the
// segment's offset in that sequence; the returned [lo, hi) are local indices
// into the segment's own slice.
func segWindow(segLen, segStart, winStart, winEnd int) (lo, hi int) {
	lo = clamp(winStart-segStart, 0, segLen)
	hi = clamp(winEnd-segStart, 0, segLen)

	if lo > hi {
		lo = hi
	}

	return lo, hi
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}

func (h *Handler) buildUserDetails(ctx context.Context) []userDetailXML {
	users, err := h.iam.ListUsers(ctx)
	if err != nil {
		return nil
	}

	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	out := make([]userDetailXML, 0, len(users))

	for i := range users {
		u := &users[i]
		detail := userDetailXML{
			Path:       u.Path,
			UserName:   u.Name,
			UserID:     u.ID,
			Arn:        u.ARN,
			CreateDate: u.CreatedAt,
			Tags:       toTagsXML(u.Tags),
		}

		if names := h.userGroupNames(ctx, u.Name); len(names) > 0 {
			detail.GroupList = &stringListXML{Member: names}
		}

		arns, _ := h.iam.ListAttachedUserPolicies(ctx, u.Name)
		detail.AttachedManagedPolicies = attachedPoliciesFromARNs(arns)
		detail.UserPolicyList = policyDetailListXML{Member: h.userInlinePolicies(ctx, u.Name)}

		out = append(out, detail)
	}

	return out
}

func (h *Handler) userGroupNames(ctx context.Context, userName string) []string {
	groups, err := h.iam.ListGroupsForUser(ctx, userName)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(groups))
	for i := range groups {
		names = append(names, groups[i].Name)
	}

	sort.Strings(names)

	return names
}

func (h *Handler) buildGroupDetails(ctx context.Context) []groupDetailXML {
	groups, err := h.iam.ListGroups(ctx)
	if err != nil {
		return nil
	}

	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })

	out := make([]groupDetailXML, 0, len(groups))

	for i := range groups {
		g := &groups[i]
		arns := h.groupAttachedPolicyARNs(ctx, g.Name)
		out = append(out, groupDetailXML{
			Path:                    g.Path,
			GroupName:               g.Name,
			GroupID:                 g.ID,
			Arn:                     g.ARN,
			CreateDate:              g.CreatedAt,
			AttachedManagedPolicies: attachedPoliciesFromARNs(arns),
			GroupPolicyList:         policyDetailListXML{Member: h.groupInlinePolicies(ctx, g.Name)},
		})
	}

	return out
}

// userInlinePolicies returns a user's inline (embedded) policies as name+document
// pairs, sorted by name. Empty when the driver does not expose inline user
// policies. It reads the same surface ListUserPolicies/GetUserPolicy expose.
func (h *Handler) userInlinePolicies(ctx context.Context, userName string) []policyDetailXML {
	pm, ok := h.userPolicies()
	if !ok {
		return nil
	}

	return inlinePolicyDetails(ctx, userName, pm.ListUserPolicies, pm.GetUserPolicy)
}

// groupInlinePolicies returns a group's inline policies as name+document pairs,
// sorted by name. It reads the same surface ListGroupPolicies/GetGroupPolicy
// expose.
func (h *Handler) groupInlinePolicies(ctx context.Context, groupName string) []policyDetailXML {
	pm, ok := h.groupPolicies()
	if !ok {
		return nil
	}

	return inlinePolicyDetails(ctx, groupName, pm.ListGroupPolicies, pm.GetGroupPolicy)
}

// groupAttachedPolicyARNs returns the ARNs of the managed policies attached to a
// group, or nil when the driver does not expose group-attached policies.
func (h *Handler) groupAttachedPolicyARNs(ctx context.Context, groupName string) []string {
	pm, ok := h.groupPolicies()
	if !ok {
		return nil
	}

	arns, err := pm.ListAttachedGroupPolicies(ctx, groupName)
	if err != nil {
		return nil
	}

	return arns
}

func (h *Handler) buildRoleDetails(ctx context.Context) []roleDetailXML {
	roles, err := h.iam.ListRoles(ctx)
	if err != nil {
		return nil
	}

	sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })

	profiles, _ := h.iam.ListInstanceProfiles(ctx)

	out := make([]roleDetailXML, 0, len(roles))

	for i := range roles {
		role := &roles[i]
		arns, _ := h.iam.ListAttachedRolePolicies(ctx, role.Name)

		out = append(out, roleDetailXML{
			Path:                     role.Path,
			RoleName:                 role.Name,
			RoleID:                   role.ID,
			Arn:                      role.ARN,
			CreateDate:               role.CreatedAt,
			AssumeRolePolicyDocument: role.AssumeRolePolicyDoc,
			InstanceProfileList:      roleInstanceProfiles(role, profiles),
			RolePolicyList:           policyDetailListXML{Member: h.roleInlinePolicies(ctx, role.Name)},
			AttachedManagedPolicies:  attachedPoliciesFromARNs(arns),
			Tags:                     toTagsXML(role.Tags),
		})
	}

	return out
}

// roleInstanceProfiles returns the profiles referencing role, each embedding the
// full role record (the shape GAAD's InstanceProfileList expects).
func roleInstanceProfiles(role *iamdriver.RoleInfo, profiles []iamdriver.InstanceProfileInfo) instanceProfilesListXML {
	out := instanceProfilesListXML{Member: []instanceProfileXML{}}

	for i := range profiles {
		if profiles[i].RoleName != role.Name {
			continue
		}

		out.Member = append(out.Member, toInstanceProfileXML(&profiles[i], role))
	}

	return out
}

// inlinePolicyDetails materializes an owner's inline policies as sorted
// name+document pairs, using the list/get pair for whichever entity kind (role,
// user, or group) the caller binds. A policy whose document can't be read is
// skipped rather than emitted empty.
func inlinePolicyDetails(ctx context.Context, owner string,
	list func(context.Context, string) ([]string, error),
	get func(context.Context, string, string) (string, error),
) []policyDetailXML {
	names, err := list(ctx, owner)
	if err != nil {
		return nil
	}

	sort.Strings(names)

	out := make([]policyDetailXML, 0, len(names))

	for _, name := range names {
		doc, err := get(ctx, owner, name)
		if err != nil {
			continue
		}

		out = append(out, policyDetailXML{PolicyName: name, PolicyDocument: doc})
	}

	return out
}

func (h *Handler) roleInlinePolicies(ctx context.Context, roleName string) []policyDetailXML {
	pm, ok := h.rolePolicies()
	if !ok {
		return nil
	}

	return inlinePolicyDetails(ctx, roleName, pm.ListRolePolicies, pm.GetRolePolicy)
}

func (h *Handler) buildPolicyDetails(ctx context.Context, filter gaadFilter) []managedPolicyDetailXML {
	if !filter.localPolicies && !filter.awsPolicies {
		return nil
	}

	policies, err := h.iam.ListPolicies(ctx)
	if err != nil {
		return nil
	}

	sort.Slice(policies, func(i, j int) bool { return policies[i].ARN < policies[j].ARN })

	out := make([]managedPolicyDetailXML, 0, len(policies))

	for i := range policies {
		p := &policies[i]

		isAWS := strings.HasPrefix(p.ARN, awsManagedPolicyPrefix)
		if isAWS && !filter.awsPolicies {
			continue
		}

		if !isAWS && !filter.localPolicies {
			continue
		}

		out = append(out, h.managedPolicyDetail(ctx, p))
	}

	return out
}

func (h *Handler) managedPolicyDetail(ctx context.Context, p *iamdriver.PolicyInfo) managedPolicyDetailXML {
	meta := h.policyMetaFor(ctx, p.ARN)

	detail := managedPolicyDetailXML{
		PolicyName:       p.Name,
		PolicyID:         p.ID,
		Arn:              p.ARN,
		Path:             p.Path,
		DefaultVersionID: meta.defaultVersionID,
		AttachmentCount:  meta.attachmentCount,
		IsAttachable:     true,
		CreateDate:       meta.createDate,
		UpdateDate:       meta.updateDate,
	}

	versions, _ := h.iam.ListPolicyVersions(ctx, p.ARN)

	detail.PolicyVersionList = policyVersionsListXML{Member: make([]policyVersionXML, 0, len(versions))}
	for i := range versions {
		detail.PolicyVersionList.Member = append(detail.PolicyVersionList.Member, toPolicyVersionXML(&versions[i], true))
	}

	return detail
}

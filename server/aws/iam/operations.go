package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// codeMalformedPolicy is the error code IAM returns when a supplied policy or
// trust-policy document is not a valid JSON object.
const codeMalformedPolicy = "MalformedPolicyDocument"

// validPolicyDocument reports whether doc is acceptable as an IAM policy or
// trust-policy document. An empty document is left to the driver's required-field
// checks; a non-empty one must parse as a JSON object.
func validPolicyDocument(doc string) bool {
	if doc == "" {
		return true
	}

	var obj map[string]any

	return json.Unmarshal([]byte(doc), &obj) == nil
}

// writeMalformedPolicy emits the IAM MalformedPolicyDocument error (HTTP 400).
func writeMalformedPolicy(w http.ResponseWriter, message string) {
	awsquery.WriteXMLError(w, http.StatusBadRequest, codeMalformedPolicy, message)
}

// defaultPolicyVersionID is the version a freshly created policy starts with.
const defaultPolicyVersionID = "v1"

// formValueTrue is the string a boolean query-protocol parameter carries when set.
const formValueTrue = "true"

// callerUserName is the synthetic IAM user name reported for the calling
// principal (GetUser with no UserName). Matches STS GetCallerIdentity.
const callerUserName = "cloudemu"

// parseIAMTags parses Tags.member.N.{Key,Value} pairs (the shape the
// aws-sdk-go-v2/service/iam client emits for tagged-create requests).
func parseIAMTags(form url.Values) map[string]string {
	indices := awsquery.CollectIndices(form, "Tags.member")
	if len(indices) == 0 {
		return nil
	}

	out := make(map[string]string, len(indices))

	for _, n := range indices {
		base := "Tags.member." + strconv.Itoa(n)
		if k := form.Get(base + ".Key"); k != "" {
			out[k] = form.Get(base + ".Value")
		}
	}

	return out
}

// --- Users ---

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	cfg := iamdriver.UserConfig{
		Name: r.Form.Get("UserName"),
		Path: r.Form.Get("Path"),
		Tags: parseIAMTags(r.Form),
	}

	u, err := h.iam.CreateUser(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	if boundary := r.Form.Get("PermissionsBoundary"); boundary != "" {
		if mgr := h.boundaryManager(); mgr != nil {
			if err := mgr.PutUserPermissionsBoundary(r.Context(), u.Name, boundary); err != nil {
				writeErr(w, err)
				return
			}
		}
	}

	out := toUserXML(u)
	out.PermissionsBoundary = h.userBoundaryXML(r.Context(), u.Name)

	awsquery.WriteXMLResponse(w, createUserResponse{
		Xmlns:    Namespace,
		Result:   createUserResult{User: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeleteUser(r.Context(), r.Form.Get("UserName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteUserResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	userName := r.Form.Get("UserName")

	// Real IAM GetUser with no UserName returns the calling principal's own
	// user. cloudemu accepts any credentials, so there is no stored caller to
	// look up; synthesize the same identity STS GetCallerIdentity reports.
	if userName == "" {
		awsquery.WriteXMLResponse(w, getUserResponse{
			Xmlns:    Namespace,
			Result:   getUserResult{User: h.callerUserXML()},
			Metadata: responseMetadata{RequestID: awsquery.RequestID},
		})

		return
	}

	u, err := h.iam.GetUser(r.Context(), userName)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := toUserXML(u)
	out.PermissionsBoundary = h.userBoundaryXML(r.Context(), u.Name)

	awsquery.WriteXMLResponse(w, getUserResponse{
		Xmlns:    Namespace,
		Result:   getUserResult{User: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// callerUserXML builds the synthetic calling-user record returned by GetUser
// when no UserName is supplied. It mirrors STS GetCallerIdentity's identity.
func (h *Handler) callerUserXML() userXML {
	return userXML{
		UserName: callerUserName,
		UserID:   "AIDACLOUDEMU0000000000",
		Arn:      "arn:aws:iam::" + h.accountID + ":user/" + callerUserName,
		Path:     "/",
	}
}

//nolint:dupl // list handlers share shape but operate on different driver types and response envelopes.
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.iam.ListUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	users = filterByPathPrefix(users, r.Form.Get("PathPrefix"), func(u *iamdriver.UserInfo) string { return u.Path })

	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	start, end, marker, truncated := pageWindow(len(users), r.Form)
	page := users[start:end]

	out := usersListXML{Member: make([]userXML, 0, len(page))}
	for i := range page {
		out.Member = append(out.Member, toUserXML(&page[i]))
	}

	awsquery.WriteXMLResponse(w, listUsersResponse{
		Xmlns:    Namespace,
		Result:   listUsersResult{Users: out, IsTruncated: truncated, Marker: marker},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- Roles ---

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
	assumeRolePolicy := r.Form.Get("AssumeRolePolicyDocument")
	if !validPolicyDocument(assumeRolePolicy) {
		writeMalformedPolicy(w, "The trust policy is not a valid JSON document")
		return
	}

	cfg := iamdriver.RoleConfig{
		Name:                r.Form.Get("RoleName"),
		Path:                r.Form.Get("Path"),
		Description:         r.Form.Get("Description"),
		AssumeRolePolicyDoc: assumeRolePolicy,
		Tags:                parseIAMTags(r.Form),
	}

	if v, err := strconv.Atoi(r.Form.Get("MaxSessionDuration")); err == nil {
		cfg.MaxSessionDuration = v
	}

	role, err := h.iam.CreateRole(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	if boundary := r.Form.Get("PermissionsBoundary"); boundary != "" {
		if mgr := h.boundaryManager(); mgr != nil {
			if err := mgr.PutRolePermissionsBoundary(r.Context(), role.Name, boundary); err != nil {
				writeErr(w, err)
				return
			}
		}
	}

	out := toRoleXML(role)
	out.PermissionsBoundary = h.roleBoundaryXML(r.Context(), role.Name)

	awsquery.WriteXMLResponse(w, createRoleResponse{
		Xmlns:    Namespace,
		Result:   createRoleResult{Role: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeleteRole(r.Context(), r.Form.Get("RoleName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRoleResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getRole(w http.ResponseWriter, r *http.Request) {
	role, err := h.iam.GetRole(r.Context(), r.Form.Get("RoleName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := toRoleXML(role)
	out.PermissionsBoundary = h.roleBoundaryXML(r.Context(), role.Name)

	awsquery.WriteXMLResponse(w, getRoleResponse{
		Xmlns:    Namespace,
		Result:   getRoleResult{Role: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // list handlers share shape but operate on different driver types and response envelopes.
func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.iam.ListRoles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	roles = filterByPathPrefix(roles, r.Form.Get("PathPrefix"), func(role *iamdriver.RoleInfo) string { return role.Path })

	sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })

	start, end, marker, truncated := pageWindow(len(roles), r.Form)
	page := roles[start:end]

	out := rolesListXML{Member: make([]roleXML, 0, len(page))}
	for i := range page {
		out.Member = append(out.Member, toRoleXML(&page[i]))
	}

	awsquery.WriteXMLResponse(w, listRolesResponse{
		Xmlns:    Namespace,
		Result:   listRolesResult{Roles: out, IsTruncated: truncated, Marker: marker},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- Policies ---

func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request) {
	if !validPolicyDocument(r.Form.Get("PolicyDocument")) {
		writeMalformedPolicy(w, "Syntax errors in policy")
		return
	}

	cfg := iamdriver.PolicyConfig{
		Name:           r.Form.Get("PolicyName"),
		Path:           r.Form.Get("Path"),
		PolicyDocument: r.Form.Get("PolicyDocument"),
		Description:    r.Form.Get("Description"),
	}

	p, err := h.iam.CreatePolicy(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createPolicyResponse{
		Xmlns:    Namespace,
		Result:   createPolicyResult{Policy: toPolicyXML(p, h.policyMetaFor(r.Context(), p.ARN))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeletePolicy(r.Context(), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deletePolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.iam.GetPolicy(r.Context(), r.Form.Get("PolicyArn"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getPolicyResponse{
		Xmlns:    Namespace,
		Result:   getPolicyResult{Policy: toPolicyXML(p, h.policyMetaFor(r.Context(), p.ARN))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.iam.ListPolicies(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	policies = h.filterPolicies(r, policies)

	sort.Slice(policies, func(i, j int) bool { return policies[i].ARN < policies[j].ARN })

	start, end, marker, truncated := pageWindow(len(policies), r.Form)
	page := policies[start:end]

	out := policiesListXML{Member: make([]policyXML, 0, len(page))}
	for i := range page {
		out.Member = append(out.Member, toPolicyXML(&page[i], h.policyMetaFor(r.Context(), page[i].ARN)))
	}

	awsquery.WriteXMLResponse(w, listPoliciesResponse{
		Xmlns:    Namespace,
		Result:   listPoliciesResult{Policies: out, IsTruncated: truncated, Marker: marker},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// filterByPathPrefix returns only the entities whose path begins with prefix,
// implementing the PathPrefix request parameter shared by ListUsers, ListRoles,
// and ListGroups. An empty prefix (the default) returns everything unchanged.
func filterByPathPrefix[T any](items []T, prefix string, path func(*T) string) []T {
	if prefix == "" {
		return items
	}

	out := items[:0]

	for i := range items {
		if strings.HasPrefix(path(&items[i]), prefix) {
			out = append(out, items[i])
		}
	}

	return out
}

// awsManagedPolicyPrefix is the ARN prefix AWS-published (managed) policies
// carry. Everything else is a customer-managed (Local) policy.
const awsManagedPolicyPrefix = "arn:aws:iam::aws:policy/"

// filterPolicies applies the ListPolicies Scope, PathPrefix, and OnlyAttached
// filters. Real IAM defaults Scope to All and OnlyAttached to false.
func (h *Handler) filterPolicies(r *http.Request, policies []iamdriver.PolicyInfo) []iamdriver.PolicyInfo {
	scope := r.Form.Get("Scope")
	pathPrefix := r.Form.Get("PathPrefix")
	onlyAttached := r.Form.Get("OnlyAttached") == formValueTrue

	out := policies[:0]

	for i := range policies {
		p := policies[i]

		isAWS := strings.HasPrefix(p.ARN, awsManagedPolicyPrefix)
		if scope == "AWS" && !isAWS {
			continue
		}

		if scope == "Local" && isAWS {
			continue
		}

		if pathPrefix != "" && !strings.HasPrefix(p.Path, pathPrefix) {
			continue
		}

		if onlyAttached && h.attachmentCount(r.Context(), p.ARN) == 0 {
			continue
		}

		out = append(out, p)
	}

	return out
}

// policyMetaFor derives the wire-only policy fields (default version, its
// create/update timestamps, and the live attachment count) for a policy ARN.
// It falls back to sensible defaults when the version list cannot be read.
func (h *Handler) policyMetaFor(ctx context.Context, policyARN string) policyMeta {
	meta := policyMeta{
		defaultVersionID: defaultPolicyVersionID,
		attachmentCount:  h.attachmentCount(ctx, policyARN),
	}

	versions, err := h.iam.ListPolicyVersions(ctx, policyARN)
	if err != nil {
		return meta
	}

	for i := range versions {
		if versions[i].VersionID == defaultPolicyVersionID {
			meta.createDate = versions[i].CreatedAt
		}

		if versions[i].IsDefaultVersion {
			meta.defaultVersionID = versions[i].VersionID
			meta.updateDate = versions[i].CreatedAt
		}
	}

	if meta.updateDate == "" {
		meta.updateDate = meta.createDate
	}

	return meta
}

// policyAttachmentCounter is the AWS-specific surface for the live count of
// principals a managed policy is attached to. It's not part of the portable
// driver, so the handler type-asserts for it.
type policyAttachmentCounter interface {
	PolicyAttachmentCount(ctx context.Context, policyARN string) (int, error)
}

// attachmentCount returns the live attachment count, or 0 when the driver does
// not expose the counter.
func (h *Handler) attachmentCount(ctx context.Context, policyARN string) int {
	counter, ok := h.iam.(policyAttachmentCounter)
	if !ok {
		return 0
	}

	n, err := counter.PolicyAttachmentCount(ctx, policyARN)
	if err != nil {
		return 0
	}

	return n
}

func (h *Handler) createPolicyVersion(w http.ResponseWriter, r *http.Request) {
	cfg := iamdriver.PolicyVersionConfig{
		PolicyARN:      r.Form.Get("PolicyArn"),
		PolicyDocument: r.Form.Get("PolicyDocument"),
		SetAsDefault:   r.Form.Get("SetAsDefault") == formValueTrue,
	}

	v, err := h.iam.CreatePolicyVersion(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createPolicyVersionResponse{
		Xmlns:    Namespace,
		Result:   createPolicyVersionResult{PolicyVersion: toPolicyVersionXML(v, true)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getPolicyVersion(w http.ResponseWriter, r *http.Request) {
	v, err := h.iam.GetPolicyVersion(r.Context(), r.Form.Get("PolicyArn"), r.Form.Get("VersionId"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getPolicyVersionResponse{
		Xmlns:    Namespace,
		Result:   getPolicyVersionResult{PolicyVersion: toPolicyVersionXML(v, true)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listPolicyVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.iam.ListPolicyVersions(r.Context(), r.Form.Get("PolicyArn"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := policyVersionsListXML{Member: make([]policyVersionXML, 0, len(versions))}
	for i := range versions {
		out.Member = append(out.Member, toPolicyVersionXML(&versions[i], false))
	}

	awsquery.WriteXMLResponse(w, listPolicyVersionsResponse{
		Xmlns:    Namespace,
		Result:   listPolicyVersionsResult{Versions: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deletePolicyVersion(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeletePolicyVersion(r.Context(), r.Form.Get("PolicyArn"), r.Form.Get("VersionId")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deletePolicyVersionResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) setDefaultPolicyVersion(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.SetDefaultPolicyVersion(r.Context(), r.Form.Get("PolicyArn"), r.Form.Get("VersionId")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, setDefaultPolicyVersionResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- Attach / Detach / ListAttached ---

func (h *Handler) attachUserPolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.AttachUserPolicy(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, attachUserPolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) detachUserPolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DetachUserPolicy(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, detachUserPolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) attachRolePolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.AttachRolePolicy(r.Context(),
		r.Form.Get("RoleName"), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, attachRolePolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) detachRolePolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DetachRolePolicy(r.Context(),
		r.Form.Get("RoleName"), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, detachRolePolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listAttachedUserPolicies(w http.ResponseWriter, r *http.Request) {
	arns, err := h.iam.ListAttachedUserPolicies(r.Context(), r.Form.Get("UserName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listAttachedUserPoliciesResponse{
		Xmlns: Namespace,
		Result: listAttachedUserPoliciesResult{
			AttachedPolicies: attachedPoliciesFromARNs(arns),
			IsTruncated:      false,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listAttachedRolePolicies(w http.ResponseWriter, r *http.Request) {
	arns, err := h.iam.ListAttachedRolePolicies(r.Context(), r.Form.Get("RoleName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listAttachedRolePoliciesResponse{
		Xmlns: Namespace,
		Result: listAttachedRolePoliciesResult{
			AttachedPolicies: attachedPoliciesFromARNs(arns),
			IsTruncated:      false,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// attachedPoliciesFromARNs turns a bare list of ARNs (what the driver returns)
// into the AttachedPolicy member shape the SDK expects. The PolicyName field
// is derived from the trailing ARN segment so the SDK has a non-empty value;
// real AWS resolves this to the canonical PolicyName.
func attachedPoliciesFromARNs(arns []string) attachedPoliciesXML {
	out := attachedPoliciesXML{Member: make([]attachedPolicyXML, 0, len(arns))}
	for _, arn := range arns {
		out.Member = append(out.Member, attachedPolicyXML{
			PolicyName: policyNameFromARN(arn),
			PolicyArn:  arn,
		})
	}

	return out
}

func policyNameFromARN(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' || arn[i] == ':' {
			return arn[i+1:]
		}
	}

	return arn
}

// --- Groups ---

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.iam.CreateGroup(r.Context(), iamdriver.GroupConfig{
		Name: r.Form.Get("GroupName"),
		Path: r.Form.Get("Path"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createGroupResponse{
		Xmlns:    Namespace,
		Result:   createGroupResult{Group: toGroupXML(g)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeleteGroup(r.Context(), r.Form.Get("GroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.iam.GetGroup(r.Context(), r.Form.Get("GroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getGroupResponse{
		Xmlns: Namespace,
		Result: getGroupResult{
			Group:       toGroupXML(g),
			Users:       h.groupMembersXML(r.Context(), g.Name),
			IsTruncated: false,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// groupMemberLister is the AWS-specific surface for a group's membership. It's
// not part of the portable driver, so the handler type-asserts for it.
type groupMemberLister interface {
	ListGroupMembers(ctx context.Context, groupName string) ([]iamdriver.UserInfo, error)
}

// groupMembersXML returns the users in a group as the wire shape GetGroup
// expects, or an empty list when the driver does not track membership.
func (h *Handler) groupMembersXML(ctx context.Context, groupName string) usersListXML {
	lister, ok := h.iam.(groupMemberLister)
	if !ok {
		return usersListXML{}
	}

	users, err := lister.ListGroupMembers(ctx, groupName)
	if err != nil {
		return usersListXML{}
	}

	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	out := usersListXML{Member: make([]userXML, 0, len(users))}
	for i := range users {
		out.Member = append(out.Member, toUserXML(&users[i]))
	}

	return out
}

//nolint:dupl // list handlers share shape but operate on different driver types and response envelopes.
func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.iam.ListGroups(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	groups = filterByPathPrefix(groups, r.Form.Get("PathPrefix"), func(g *iamdriver.GroupInfo) string { return g.Path })

	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })

	start, end, marker, truncated := pageWindow(len(groups), r.Form)
	page := groups[start:end]

	out := groupsListXML{Member: make([]groupXML, 0, len(page))}
	for i := range page {
		out.Member = append(out.Member, toGroupXML(&page[i]))
	}

	awsquery.WriteXMLResponse(w, listGroupsResponse{
		Xmlns:    Namespace,
		Result:   listGroupsResult{Groups: out, IsTruncated: truncated, Marker: marker},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) addUserToGroup(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.AddUserToGroup(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("GroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, addUserToGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) removeUserFromGroup(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.RemoveUserFromGroup(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("GroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removeUserFromGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listGroupsForUser(w http.ResponseWriter, r *http.Request) {
	groups, err := h.iam.ListGroupsForUser(r.Context(), r.Form.Get("UserName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := groupsListXML{Member: make([]groupXML, 0, len(groups))}
	for i := range groups {
		out.Member = append(out.Member, toGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, listGroupsForUserResponse{
		Xmlns:    Namespace,
		Result:   listGroupsForUserResult{Groups: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- AccessKeys ---

func (h *Handler) createAccessKey(w http.ResponseWriter, r *http.Request) {
	k, err := h.iam.CreateAccessKey(r.Context(), iamdriver.AccessKeyConfig{
		UserName: r.Form.Get("UserName"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createAccessKeyResponse{
		Xmlns:    Namespace,
		Result:   createAccessKeyResult{AccessKey: toAccessKeyXML(k)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteAccessKey(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeleteAccessKey(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("AccessKeyId")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteAccessKeyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listAccessKeys(w http.ResponseWriter, r *http.Request) {
	user := r.Form.Get("UserName")

	keys, err := h.iam.ListAccessKeys(r.Context(), user)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := accessKeyMetadataListXML{Member: make([]accessKeyMetadataXML, 0, len(keys))}
	for i := range keys {
		out.Member = append(out.Member, toAccessKeyMetadataXML(&keys[i]))
	}

	awsquery.WriteXMLResponse(w, listAccessKeysResponse{
		Xmlns: Namespace,
		Result: listAccessKeysResult{
			UserName:          user,
			AccessKeyMetadata: out,
			IsTruncated:       false,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- Instance Profiles ---

func (h *Handler) createInstanceProfile(w http.ResponseWriter, r *http.Request) {
	cfg := iamdriver.InstanceProfileConfig{
		Name: r.Form.Get("InstanceProfileName"),
		Path: r.Form.Get("Path"),
		Tags: parseIAMTags(r.Form),
	}

	p, err := h.iam.CreateInstanceProfile(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createInstanceProfileResponse{
		Xmlns:    Namespace,
		Result:   createInstanceProfileResult{InstanceProfile: toInstanceProfileXML(p, nil)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteInstanceProfile(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.DeleteInstanceProfile(r.Context(), r.Form.Get("InstanceProfileName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteInstanceProfileResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getInstanceProfile(w http.ResponseWriter, r *http.Request) {
	p, err := h.iam.GetInstanceProfile(r.Context(), r.Form.Get("InstanceProfileName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getInstanceProfileResponse{
		Xmlns:    Namespace,
		Result:   getInstanceProfileResult{InstanceProfile: toInstanceProfileXML(p, h.lookupRole(r, p.RoleName))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listInstanceProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.iam.ListInstanceProfiles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := instanceProfilesListXML{Member: make([]instanceProfileXML, 0, len(profiles))}
	for i := range profiles {
		out.Member = append(out.Member, toInstanceProfileXML(&profiles[i], h.lookupRole(r, profiles[i].RoleName)))
	}

	awsquery.WriteXMLResponse(w, listInstanceProfilesResponse{
		Xmlns:    Namespace,
		Result:   listInstanceProfilesResult{InstanceProfiles: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) addRoleToInstanceProfile(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.AddRoleToInstanceProfile(r.Context(),
		r.Form.Get("InstanceProfileName"), r.Form.Get("RoleName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, addRoleToInstanceProfileResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) removeRoleFromInstanceProfile(w http.ResponseWriter, r *http.Request) {
	if err := h.iam.RemoveRoleFromInstanceProfile(r.Context(),
		r.Form.Get("InstanceProfileName"), r.Form.Get("RoleName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removeRoleFromInstanceProfileResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// lookupRole resolves a role name to RoleInfo for embedding in InstanceProfile
// responses. Returns nil if the role doesn't exist — the caller falls back to
// emitting a minimal Role with just the name.
//
// listInstanceProfiles calls this once per profile (an N+1 driver hop). This
// is acceptable for an in-memory emulator at the scales it targets (tens of
// profiles in a test); rewriting as a single bulk-fetch would be premature
// optimization given the driver lacks a batch API.
func (h *Handler) lookupRole(r *http.Request, name string) *iamdriver.RoleInfo {
	if name == "" {
		return nil
	}

	role, err := h.iam.GetRole(r.Context(), name)
	if err != nil {
		return nil
	}

	return role
}

// listInstanceProfilesForRole returns the instance profiles that reference the
// given role. It filters the full profile list by RoleName, which is how the
// association is stored (AddRoleToInstanceProfile sets the profile's role).
func (h *Handler) listInstanceProfilesForRole(w http.ResponseWriter, r *http.Request) {
	roleName := r.Form.Get("RoleName")

	profiles, err := h.iam.ListInstanceProfiles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := instanceProfilesListXML{Member: []instanceProfileXML{}}

	for i := range profiles {
		if profiles[i].RoleName != roleName {
			continue
		}

		out.Member = append(out.Member, toInstanceProfileXML(&profiles[i], h.lookupRole(r, profiles[i].RoleName)))
	}

	awsquery.WriteXMLResponse(w, listInstanceProfilesForRoleResponse{
		Xmlns: Namespace,
		Result: listInstanceProfilesForRoleResult{
			InstanceProfiles: out,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

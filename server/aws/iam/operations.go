package iam

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// defaultPolicyVersionID is the version a freshly created policy starts with.
const defaultPolicyVersionID = "v1"

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

	awsquery.WriteXMLResponse(w, createUserResponse{
		Xmlns:    Namespace,
		Result:   createUserResult{User: toUserXML(u)},
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
	u, err := h.iam.GetUser(r.Context(), r.Form.Get("UserName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getUserResponse{
		Xmlns:    Namespace,
		Result:   getUserResult{User: toUserXML(u)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // list handlers share shape but operate on different driver types and response envelopes.
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.iam.ListUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := usersListXML{Member: make([]userXML, 0, len(users))}
	for i := range users {
		out.Member = append(out.Member, toUserXML(&users[i]))
	}

	awsquery.WriteXMLResponse(w, listUsersResponse{
		Xmlns:    Namespace,
		Result:   listUsersResult{Users: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- Roles ---

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
	cfg := iamdriver.RoleConfig{
		Name:                r.Form.Get("RoleName"),
		Path:                r.Form.Get("Path"),
		AssumeRolePolicyDoc: r.Form.Get("AssumeRolePolicyDocument"),
		Tags:                parseIAMTags(r.Form),
	}

	role, err := h.iam.CreateRole(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createRoleResponse{
		Xmlns:    Namespace,
		Result:   createRoleResult{Role: toRoleXML(role)},
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

	awsquery.WriteXMLResponse(w, getRoleResponse{
		Xmlns:    Namespace,
		Result:   getRoleResult{Role: toRoleXML(role)},
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

	out := rolesListXML{Member: make([]roleXML, 0, len(roles))}
	for i := range roles {
		out.Member = append(out.Member, toRoleXML(&roles[i]))
	}

	awsquery.WriteXMLResponse(w, listRolesResponse{
		Xmlns:    Namespace,
		Result:   listRolesResult{Roles: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// --- Policies ---

func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request) {
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
		Result:   createPolicyResult{Policy: toPolicyXML(p, defaultPolicyVersionID)},
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
		Result:   getPolicyResult{Policy: toPolicyXML(p, h.defaultVersionID(r.Context(), p.ARN))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.iam.ListPolicies(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := policiesListXML{Member: make([]policyXML, 0, len(policies))}
	for i := range policies {
		out.Member = append(out.Member, toPolicyXML(&policies[i], h.defaultVersionID(r.Context(), policies[i].ARN)))
	}

	awsquery.WriteXMLResponse(w, listPoliciesResponse{
		Xmlns:    Namespace,
		Result:   listPoliciesResult{Policies: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// defaultVersionID returns the version ID currently marked default for the
// policy, falling back to "v1" if the versions cannot be read.
func (h *Handler) defaultVersionID(ctx context.Context, policyARN string) string {
	versions, err := h.iam.ListPolicyVersions(ctx, policyARN)
	if err != nil {
		return defaultPolicyVersionID
	}

	for i := range versions {
		if versions[i].IsDefaultVersion {
			return versions[i].VersionID
		}
	}

	return defaultPolicyVersionID
}

func (h *Handler) createPolicyVersion(w http.ResponseWriter, r *http.Request) {
	cfg := iamdriver.PolicyVersionConfig{
		PolicyARN:      r.Form.Get("PolicyArn"),
		PolicyDocument: r.Form.Get("PolicyDocument"),
		SetAsDefault:   r.Form.Get("SetAsDefault") == "true",
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
			Users:       usersListXML{},
			IsTruncated: false,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // list handlers share shape but operate on different driver types and response envelopes.
func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.iam.ListGroups(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := groupsListXML{Member: make([]groupXML, 0, len(groups))}
	for i := range groups {
		out.Member = append(out.Member, toGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, listGroupsResponse{
		Xmlns:    Namespace,
		Result:   listGroupsResult{Groups: out, IsTruncated: false},
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

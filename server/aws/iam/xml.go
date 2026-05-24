package iam

import (
	"encoding/xml"

	iamdriver "github.com/stackshy/cloudemu/iam/driver"
)

// Every IAM query-protocol response is wrapped in <FooResponse xmlns="..."> with a
// <FooResult> child and a trailing <ResponseMetadata><RequestId>...</RequestId>
// </ResponseMetadata>. The structures below mirror the AWS-published XML
// closely enough that aws-sdk-go-v2's IAM unmarshalers consume them.

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type tagsXML struct {
	Member []tagXML `xml:"member,omitempty"`
}

func toTagsXML(tags map[string]string) *tagsXML {
	if len(tags) == 0 {
		return nil
	}

	out := &tagsXML{Member: make([]tagXML, 0, len(tags))}
	for k, v := range tags {
		out.Member = append(out.Member, tagXML{Key: k, Value: v})
	}

	return out
}

type userXML struct {
	UserName   string   `xml:"UserName"`
	UserID     string   `xml:"UserId"`
	Arn        string   `xml:"Arn"`
	Path       string   `xml:"Path,omitempty"`
	CreateDate string   `xml:"CreateDate,omitempty"`
	Tags       *tagsXML `xml:"Tags,omitempty"`
}

func toUserXML(u *iamdriver.UserInfo) userXML {
	return userXML{
		UserName:   u.Name,
		UserID:     u.ID,
		Arn:        u.ARN,
		Path:       u.Path,
		CreateDate: u.CreatedAt,
		Tags:       toTagsXML(u.Tags),
	}
}

type roleXML struct {
	RoleName                 string   `xml:"RoleName"`
	RoleID                   string   `xml:"RoleId"`
	Arn                      string   `xml:"Arn"`
	Path                     string   `xml:"Path,omitempty"`
	AssumeRolePolicyDocument string   `xml:"AssumeRolePolicyDocument,omitempty"`
	Tags                     *tagsXML `xml:"Tags,omitempty"`
}

func toRoleXML(r *iamdriver.RoleInfo) roleXML {
	return roleXML{
		RoleName:                 r.Name,
		RoleID:                   r.ID,
		Arn:                      r.ARN,
		Path:                     r.Path,
		AssumeRolePolicyDocument: r.AssumeRolePolicyDoc,
		Tags:                     toTagsXML(r.Tags),
	}
}

type policyXML struct {
	PolicyName       string `xml:"PolicyName"`
	PolicyID         string `xml:"PolicyId"`
	Arn              string `xml:"Arn"`
	Path             string `xml:"Path,omitempty"`
	DefaultVersionID string `xml:"DefaultVersionId,omitempty"`
	AttachmentCount  int    `xml:"AttachmentCount"`
	IsAttachable     bool   `xml:"IsAttachable"`
	Description      string `xml:"Description,omitempty"`
}

func toPolicyXML(p *iamdriver.PolicyInfo) policyXML {
	return policyXML{
		PolicyName:       p.Name,
		PolicyID:         p.ID,
		Arn:              p.ARN,
		Path:             p.Path,
		DefaultVersionID: "v1",
		AttachmentCount:  0,
		IsAttachable:     true,
		Description:      p.Description,
	}
}

type groupXML struct {
	GroupName  string `xml:"GroupName"`
	GroupID    string `xml:"GroupId,omitempty"`
	Arn        string `xml:"Arn"`
	Path       string `xml:"Path,omitempty"`
	CreateDate string `xml:"CreateDate,omitempty"`
}

func toGroupXML(g *iamdriver.GroupInfo) groupXML {
	return groupXML{
		GroupName:  g.Name,
		Arn:        g.ARN,
		Path:       g.Path,
		CreateDate: g.CreatedAt,
	}
}

type accessKeyXML struct {
	UserName        string `xml:"UserName"`
	AccessKeyID     string `xml:"AccessKeyId"`
	Status          string `xml:"Status"`
	SecretAccessKey string `xml:"SecretAccessKey,omitempty"`
	CreateDate      string `xml:"CreateDate,omitempty"`
}

func toAccessKeyXML(k *iamdriver.AccessKeyInfo) accessKeyXML {
	return accessKeyXML{
		UserName:        k.UserName,
		AccessKeyID:     k.AccessKeyID,
		Status:          k.Status,
		SecretAccessKey: k.SecretAccessKey,
		CreateDate:      k.CreatedAt,
	}
}

type accessKeyMetadataXML struct {
	UserName    string `xml:"UserName"`
	AccessKeyID string `xml:"AccessKeyId"`
	Status      string `xml:"Status"`
	CreateDate  string `xml:"CreateDate,omitempty"`
}

func toAccessKeyMetadataXML(k *iamdriver.AccessKeyInfo) accessKeyMetadataXML {
	return accessKeyMetadataXML{
		UserName:    k.UserName,
		AccessKeyID: k.AccessKeyID,
		Status:      k.Status,
		CreateDate:  k.CreatedAt,
	}
}

type instanceProfileRolesXML struct {
	Member []roleXML `xml:"member,omitempty"`
}

type instanceProfileXML struct {
	InstanceProfileName string                   `xml:"InstanceProfileName"`
	InstanceProfileID   string                   `xml:"InstanceProfileId"`
	Arn                 string                   `xml:"Arn"`
	Path                string                   `xml:"Path,omitempty"`
	CreateDate          string                   `xml:"CreateDate,omitempty"`
	Roles               *instanceProfileRolesXML `xml:"Roles,omitempty"`
	Tags                *tagsXML                 `xml:"Tags,omitempty"`
}

func toInstanceProfileXML(p *iamdriver.InstanceProfileInfo, role *iamdriver.RoleInfo) instanceProfileXML {
	out := instanceProfileXML{
		InstanceProfileName: p.Name,
		InstanceProfileID:   p.ID,
		Arn:                 p.ARN,
		CreateDate:          p.CreatedAt,
		Tags:                toTagsXML(p.Tags),
	}

	if role != nil {
		out.Roles = &instanceProfileRolesXML{Member: []roleXML{toRoleXML(role)}}
	} else if p.RoleName != "" {
		out.Roles = &instanceProfileRolesXML{Member: []roleXML{{RoleName: p.RoleName}}}
	}

	return out
}

type attachedPolicyXML struct {
	PolicyName string `xml:"PolicyName"`
	PolicyArn  string `xml:"PolicyArn"`
}

type attachedPoliciesXML struct {
	Member []attachedPolicyXML `xml:"member,omitempty"`
}

type usersListXML struct {
	Member []userXML `xml:"member,omitempty"`
}

type rolesListXML struct {
	Member []roleXML `xml:"member,omitempty"`
}

type policiesListXML struct {
	Member []policyXML `xml:"member,omitempty"`
}

type groupsListXML struct {
	Member []groupXML `xml:"member,omitempty"`
}

type accessKeyMetadataListXML struct {
	Member []accessKeyMetadataXML `xml:"member,omitempty"`
}

type instanceProfilesListXML struct {
	Member []instanceProfileXML `xml:"member,omitempty"`
}

// Response envelopes. Each pairs an XMLName forcing the outer element name
// with a result element and the shared ResponseMetadata tail.

type createUserResponse struct {
	XMLName  xml.Name         `xml:"CreateUserResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   createUserResult `xml:"CreateUserResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type createUserResult struct {
	User userXML `xml:"User"`
}

type deleteUserResponse struct {
	XMLName  xml.Name         `xml:"DeleteUserResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getUserResponse struct {
	XMLName  xml.Name         `xml:"GetUserResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   getUserResult    `xml:"GetUserResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getUserResult struct {
	User userXML `xml:"User"`
}

type listUsersResponse struct {
	XMLName  xml.Name         `xml:"ListUsersResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listUsersResult  `xml:"ListUsersResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listUsersResult struct {
	Users       usersListXML `xml:"Users"`
	IsTruncated bool         `xml:"IsTruncated"`
}

type createRoleResponse struct {
	XMLName  xml.Name         `xml:"CreateRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   createRoleResult `xml:"CreateRoleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type createRoleResult struct {
	Role roleXML `xml:"Role"`
}

type deleteRoleResponse struct {
	XMLName  xml.Name         `xml:"DeleteRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getRoleResponse struct {
	XMLName  xml.Name         `xml:"GetRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   getRoleResult    `xml:"GetRoleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getRoleResult struct {
	Role roleXML `xml:"Role"`
}

type listRolesResponse struct {
	XMLName  xml.Name         `xml:"ListRolesResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listRolesResult  `xml:"ListRolesResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listRolesResult struct {
	Roles       rolesListXML `xml:"Roles"`
	IsTruncated bool         `xml:"IsTruncated"`
}

type createPolicyResponse struct {
	XMLName  xml.Name           `xml:"CreatePolicyResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   createPolicyResult `xml:"CreatePolicyResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type createPolicyResult struct {
	Policy policyXML `xml:"Policy"`
}

type deletePolicyResponse struct {
	XMLName  xml.Name         `xml:"DeletePolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getPolicyResponse struct {
	XMLName  xml.Name         `xml:"GetPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   getPolicyResult  `xml:"GetPolicyResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getPolicyResult struct {
	Policy policyXML `xml:"Policy"`
}

type listPoliciesResponse struct {
	XMLName  xml.Name           `xml:"ListPoliciesResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   listPoliciesResult `xml:"ListPoliciesResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type listPoliciesResult struct {
	Policies    policiesListXML `xml:"Policies"`
	IsTruncated bool            `xml:"IsTruncated"`
}

type attachUserPolicyResponse struct {
	XMLName  xml.Name         `xml:"AttachUserPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type detachUserPolicyResponse struct {
	XMLName  xml.Name         `xml:"DetachUserPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type attachRolePolicyResponse struct {
	XMLName  xml.Name         `xml:"AttachRolePolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type detachRolePolicyResponse struct {
	XMLName  xml.Name         `xml:"DetachRolePolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listAttachedUserPoliciesResponse struct {
	XMLName  xml.Name                       `xml:"ListAttachedUserPoliciesResponse"`
	Xmlns    string                         `xml:"xmlns,attr"`
	Result   listAttachedUserPoliciesResult `xml:"ListAttachedUserPoliciesResult"`
	Metadata responseMetadata               `xml:"ResponseMetadata"`
}

type listAttachedUserPoliciesResult struct {
	AttachedPolicies attachedPoliciesXML `xml:"AttachedPolicies"`
	IsTruncated      bool                `xml:"IsTruncated"`
}

type listAttachedRolePoliciesResponse struct {
	XMLName  xml.Name                       `xml:"ListAttachedRolePoliciesResponse"`
	Xmlns    string                         `xml:"xmlns,attr"`
	Result   listAttachedRolePoliciesResult `xml:"ListAttachedRolePoliciesResult"`
	Metadata responseMetadata               `xml:"ResponseMetadata"`
}

type listAttachedRolePoliciesResult struct {
	AttachedPolicies attachedPoliciesXML `xml:"AttachedPolicies"`
	IsTruncated      bool                `xml:"IsTruncated"`
}

type createGroupResponse struct {
	XMLName  xml.Name          `xml:"CreateGroupResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   createGroupResult `xml:"CreateGroupResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type createGroupResult struct {
	Group groupXML `xml:"Group"`
}

type deleteGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getGroupResponse struct {
	XMLName  xml.Name         `xml:"GetGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   getGroupResult   `xml:"GetGroupResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getGroupResult struct {
	Group       groupXML     `xml:"Group"`
	Users       usersListXML `xml:"Users"`
	IsTruncated bool         `xml:"IsTruncated"`
}

type listGroupsResponse struct {
	XMLName  xml.Name         `xml:"ListGroupsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listGroupsResult `xml:"ListGroupsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listGroupsResult struct {
	Groups      groupsListXML `xml:"Groups"`
	IsTruncated bool          `xml:"IsTruncated"`
}

type addUserToGroupResponse struct {
	XMLName  xml.Name         `xml:"AddUserToGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type removeUserFromGroupResponse struct {
	XMLName  xml.Name         `xml:"RemoveUserFromGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listGroupsForUserResponse struct {
	XMLName  xml.Name                `xml:"ListGroupsForUserResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   listGroupsForUserResult `xml:"ListGroupsForUserResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type listGroupsForUserResult struct {
	Groups      groupsListXML `xml:"Groups"`
	IsTruncated bool          `xml:"IsTruncated"`
}

type createAccessKeyResponse struct {
	XMLName  xml.Name              `xml:"CreateAccessKeyResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   createAccessKeyResult `xml:"CreateAccessKeyResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type createAccessKeyResult struct {
	AccessKey accessKeyXML `xml:"AccessKey"`
}

type deleteAccessKeyResponse struct {
	XMLName  xml.Name         `xml:"DeleteAccessKeyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listAccessKeysResponse struct {
	XMLName  xml.Name             `xml:"ListAccessKeysResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   listAccessKeysResult `xml:"ListAccessKeysResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type listAccessKeysResult struct {
	UserName          string                   `xml:"UserName,omitempty"`
	AccessKeyMetadata accessKeyMetadataListXML `xml:"AccessKeyMetadata"`
	IsTruncated       bool                     `xml:"IsTruncated"`
}

type createInstanceProfileResponse struct {
	XMLName  xml.Name                    `xml:"CreateInstanceProfileResponse"`
	Xmlns    string                      `xml:"xmlns,attr"`
	Result   createInstanceProfileResult `xml:"CreateInstanceProfileResult"`
	Metadata responseMetadata            `xml:"ResponseMetadata"`
}

type createInstanceProfileResult struct {
	InstanceProfile instanceProfileXML `xml:"InstanceProfile"`
}

type deleteInstanceProfileResponse struct {
	XMLName  xml.Name         `xml:"DeleteInstanceProfileResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getInstanceProfileResponse struct {
	XMLName  xml.Name                 `xml:"GetInstanceProfileResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   getInstanceProfileResult `xml:"GetInstanceProfileResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type getInstanceProfileResult struct {
	InstanceProfile instanceProfileXML `xml:"InstanceProfile"`
}

type listInstanceProfilesResponse struct {
	XMLName  xml.Name                   `xml:"ListInstanceProfilesResponse"`
	Xmlns    string                     `xml:"xmlns,attr"`
	Result   listInstanceProfilesResult `xml:"ListInstanceProfilesResult"`
	Metadata responseMetadata           `xml:"ResponseMetadata"`
}

type listInstanceProfilesResult struct {
	InstanceProfiles instanceProfilesListXML `xml:"InstanceProfiles"`
	IsTruncated      bool                    `xml:"IsTruncated"`
}

type addRoleToInstanceProfileResponse struct {
	XMLName  xml.Name         `xml:"AddRoleToInstanceProfileResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type removeRoleFromInstanceProfileResponse struct {
	XMLName  xml.Name         `xml:"RemoveRoleFromInstanceProfileResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

package iam

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// policyEntityLister is the AWS-specific reverse-lookup surface behind
// ListEntitiesForPolicy. It's not part of the portable IAM driver, so the
// handler type-asserts for it.
type policyEntityLister interface {
	ListEntitiesForPolicy(ctx context.Context, policyARN string) (iamdriver.PolicyEntities, error)
}

type policyUserXML struct {
	UserName string `xml:"UserName"`
	UserID   string `xml:"UserId"`
}

type policyGroupXML struct {
	GroupName string `xml:"GroupName"`
	GroupID   string `xml:"GroupId"`
}

type policyRoleXML struct {
	RoleName string `xml:"RoleName"`
	RoleID   string `xml:"RoleId"`
}

type policyUsersXML struct {
	Member []policyUserXML `xml:"member,omitempty"`
}

type policyGroupsXML struct {
	Member []policyGroupXML `xml:"member,omitempty"`
}

type policyRolesXML struct {
	Member []policyRoleXML `xml:"member,omitempty"`
}

type listEntitiesForPolicyResult struct {
	PolicyUsers  policyUsersXML  `xml:"PolicyUsers"`
	PolicyGroups policyGroupsXML `xml:"PolicyGroups"`
	PolicyRoles  policyRolesXML  `xml:"PolicyRoles"`
	IsTruncated  bool            `xml:"IsTruncated"`
	Marker       string          `xml:"Marker,omitempty"`
}

type listEntitiesForPolicyResponse struct {
	XMLName  xml.Name                    `xml:"ListEntitiesForPolicyResponse"`
	Xmlns    string                      `xml:"xmlns,attr"`
	Result   listEntitiesForPolicyResult `xml:"ListEntitiesForPolicyResult"`
	Metadata responseMetadata            `xml:"ResponseMetadata"`
}

// entityRow is one principal in the flattened, ordered view the handler
// paginates over. typ is "User", "Group", or "Role".
type entityRow struct {
	typ  string
	name string
	id   string
}

func (h *Handler) listEntitiesForPolicy(w http.ResponseWriter, r *http.Request) {
	lister, ok := h.iam.(policyEntityLister)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "ListEntitiesForPolicy not supported"))
		return
	}

	entities, err := lister.ListEntitiesForPolicy(r.Context(), r.Form.Get("PolicyArn"))
	if err != nil {
		writeErr(w, err)
		return
	}

	rows := flattenEntities(entities, r.Form.Get("EntityFilter"), r.Form.Get("PathPrefix"))

	start, end, marker, truncated := pageWindow(len(rows), r.Form)
	page := rows[start:end]

	awsquery.WriteXMLResponse(w, listEntitiesForPolicyResponse{
		Xmlns:    Namespace,
		Result:   buildEntitiesResult(page, truncated, marker),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// flattenEntities applies the EntityFilter and PathPrefix filters, then returns
// a single deterministically ordered slice (users, then groups, then roles,
// each sorted by name) so pagination yields a stable window with one Marker.
func flattenEntities(e iamdriver.PolicyEntities, entityFilter, pathPrefix string) []entityRow {
	rows := make([]entityRow, 0, len(e.Users)+len(e.Groups)+len(e.Roles))

	if includeEntity(entityFilter, "User") {
		rows = appendEntityRows(rows, "User", e.Users, pathPrefix)
	}

	if includeEntity(entityFilter, "Group") {
		rows = appendEntityRows(rows, "Group", e.Groups, pathPrefix)
	}

	if includeEntity(entityFilter, "Role") {
		rows = appendEntityRows(rows, "Role", e.Roles, pathPrefix)
	}

	return rows
}

func appendEntityRows(rows []entityRow, typ string, ents []iamdriver.PolicyEntity, pathPrefix string) []entityRow {
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name < ents[j].Name })

	for i := range ents {
		if pathPrefix != "" && !strings.HasPrefix(ents[i].Path, pathPrefix) {
			continue
		}

		rows = append(rows, entityRow{typ: typ, name: ents[i].Name, id: ents[i].ID})
	}

	return rows
}

// includeEntity reports whether an entity type is selected by EntityFilter. An
// empty filter selects all types; otherwise it selects only the named type
// (User, Role, Group). LocalManagedPolicy/AWSManagedPolicy filters never match a
// principal type, so they return no entities.
func includeEntity(entityFilter, typ string) bool {
	return entityFilter == "" || entityFilter == typ
}

func buildEntitiesResult(page []entityRow, truncated bool, marker string) listEntitiesForPolicyResult {
	res := listEntitiesForPolicyResult{IsTruncated: truncated, Marker: marker}

	for _, row := range page {
		switch row.typ {
		case "User":
			res.PolicyUsers.Member = append(res.PolicyUsers.Member,
				policyUserXML{UserName: row.name, UserID: row.id})
		case "Group":
			res.PolicyGroups.Member = append(res.PolicyGroups.Member,
				policyGroupXML{GroupName: row.name, GroupID: row.id})
		case "Role":
			res.PolicyRoles.Member = append(res.PolicyRoles.Member,
				policyRoleXML{RoleName: row.name, RoleID: row.id})
		}
	}

	return res
}

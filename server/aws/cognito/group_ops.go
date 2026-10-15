package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// groupJSON is the GroupType shape.
type groupJSON struct {
	GroupName        string   `json:"GroupName"`
	UserPoolID       string   `json:"UserPoolId"`
	Description      string   `json:"Description,omitempty"`
	RoleArn          string   `json:"RoleArn,omitempty"`
	Precedence       *int32   `json:"Precedence,omitempty"`
	LastModifiedDate *float64 `json:"LastModifiedDate,omitempty"`
	CreationDate     *float64 `json:"CreationDate,omitempty"`
}

func groupToWire(g *driver.Group) groupJSON {
	return groupJSON{
		GroupName:        g.GroupName,
		UserPoolID:       g.UserPoolID,
		Description:      g.Description,
		RoleArn:          g.RoleARN,
		Precedence:       g.Precedence,
		LastModifiedDate: epochOrNil(g.LastModifiedDate),
		CreationDate:     epochOrNil(g.CreationDate),
	}
}

func groupsToWire(in []driver.Group) []groupJSON {
	out := make([]groupJSON, len(in))
	for i := range in {
		out[i] = groupToWire(&in[i])
	}

	return out
}

type groupResponse struct {
	Group groupJSON `json:"Group"`
}

func groupResult(g *driver.Group, err error) (any, error) {
	if err != nil {
		return nil, err
	}

	return groupResponse{Group: groupToWire(g)}, nil
}

type createGroupRequest struct {
	UserPoolID  string `json:"UserPoolId"`
	GroupName   string `json:"GroupName"`
	Description string `json:"Description"`
	RoleArn     string `json:"RoleArn"`
	Precedence  *int32 `json:"Precedence"`
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createGroupRequest) (any, error) {
		return groupResult(h.cognito.CreateGroup(ctx, driver.CreateGroupInput{
			UserPoolID: req.UserPoolID, GroupName: req.GroupName, Description: req.Description,
			RoleARN: req.RoleArn, Precedence: req.Precedence,
		}))
	})
}

type groupRef struct {
	UserPoolID string `json:"UserPoolId"`
	GroupName  string `json:"GroupName"`
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *groupRef) (any, error) {
		return groupResult(h.cognito.GetGroup(ctx, req.UserPoolID, req.GroupName))
	})
}

type updateGroupRequest struct {
	UserPoolID  string  `json:"UserPoolId"`
	GroupName   string  `json:"GroupName"`
	Description *string `json:"Description"`
	RoleArn     *string `json:"RoleArn"`
	Precedence  *int32  `json:"Precedence"`
}

func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateGroupRequest) (any, error) {
		return groupResult(h.cognito.UpdateGroup(ctx, driver.UpdateGroupInput{
			UserPoolID: req.UserPoolID, GroupName: req.GroupName, Description: req.Description,
			RoleARN: req.RoleArn, Precedence: req.Precedence,
		}))
	})
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *groupRef) (any, error) {
		if err := h.cognito.DeleteGroup(ctx, req.UserPoolID, req.GroupName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listGroupsRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
	GroupName  string `json:"GroupName"`
	Limit      int32  `json:"Limit"`
	NextToken  string `json:"NextToken"`
}

func (req *listGroupsRequest) page() driver.Pagination {
	return driver.Pagination{NextToken: req.NextToken, MaxResults: req.Limit}
}

type listGroupsResponse struct {
	Groups    []groupJSON `json:"Groups"`
	NextToken string      `json:"NextToken,omitempty"`
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listGroupsRequest) (any, error) {
		groups, next, err := h.cognito.ListGroups(ctx, req.UserPoolID, req.page())
		if err != nil {
			return nil, err
		}

		return listGroupsResponse{Groups: groupsToWire(groups), NextToken: next}, nil
	})
}

func (h *Handler) adminListGroupsForUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listGroupsRequest) (any, error) {
		groups, next, err := h.cognito.AdminListGroupsForUser(ctx, req.UserPoolID, req.Username, req.page())
		if err != nil {
			return nil, err
		}

		return listGroupsResponse{Groups: groupsToWire(groups), NextToken: next}, nil
	})
}

type listUsersInGroupResponse struct {
	Users     []userTypeJSON `json:"Users"`
	NextToken string         `json:"NextToken,omitempty"`
}

func (h *Handler) listUsersInGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listGroupsRequest) (any, error) {
		users, next, err := h.cognito.ListUsersInGroup(ctx, req.UserPoolID, req.GroupName, req.page())
		if err != nil {
			return nil, err
		}

		out := make([]userTypeJSON, len(users))
		for i := range users {
			out[i] = userToWire(&users[i])
		}

		return listUsersInGroupResponse{Users: out, NextToken: next}, nil
	})
}

type userGroupRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
	GroupName  string `json:"GroupName"`
}

func (h *Handler) adminAddUserToGroup(w http.ResponseWriter, r *http.Request) {
	h.membershipOp(w, r, h.cognito.AdminAddUserToGroup)
}

func (h *Handler) adminRemoveUserFromGroup(w http.ResponseWriter, r *http.Request) {
	h.membershipOp(w, r, h.cognito.AdminRemoveUserFromGroup)
}

func (h *Handler) membershipOp(w http.ResponseWriter, r *http.Request, call func(context.Context, string, string, string) error) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *userGroupRequest) (any, error) {
		if err := call(ctx, req.UserPoolID, req.Username, req.GroupName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

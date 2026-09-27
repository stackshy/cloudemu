package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

type attributeJSON struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// userTypeJSON is the UserType shape AdminCreateUser and ListUsers return.
type userTypeJSON struct {
	Username             string          `json:"Username"`
	Attributes           []attributeJSON `json:"Attributes"`
	UserCreateDate       *float64        `json:"UserCreateDate,omitempty"`
	UserLastModifiedDate *float64        `json:"UserLastModifiedDate,omitempty"`
	Enabled              bool            `json:"Enabled"`
	UserStatus           string          `json:"UserStatus,omitempty"`
}

// adminGetUserResponse is AdminGetUser's flat output; its attribute list is
// named UserAttributes rather than Attributes.
type adminGetUserResponse struct {
	Username             string          `json:"Username"`
	UserAttributes       []attributeJSON `json:"UserAttributes"`
	UserCreateDate       *float64        `json:"UserCreateDate,omitempty"`
	UserLastModifiedDate *float64        `json:"UserLastModifiedDate,omitempty"`
	Enabled              bool            `json:"Enabled"`
	UserStatus           string          `json:"UserStatus,omitempty"`
}

func attributesToWire(in []driver.Attribute) []attributeJSON {
	out := make([]attributeJSON, len(in))
	for i, a := range in {
		out[i] = attributeJSON(a)
	}

	return out
}

func attributesFromWire(in []attributeJSON) []driver.Attribute {
	if in == nil {
		return nil
	}

	out := make([]driver.Attribute, len(in))
	for i, a := range in {
		out[i] = driver.Attribute(a)
	}

	return out
}

func userToWire(u *driver.User) userTypeJSON {
	return userTypeJSON{
		Username:             u.Username,
		Attributes:           attributesToWire(u.Attributes),
		UserCreateDate:       epochOrNil(u.UserCreateDate),
		UserLastModifiedDate: epochOrNil(u.UserLastModifiedDate),
		Enabled:              u.Enabled,
		UserStatus:           u.UserStatus,
	}
}

type adminCreateUserRequest struct {
	UserPoolID             string          `json:"UserPoolId"`
	Username               string          `json:"Username"`
	UserAttributes         []attributeJSON `json:"UserAttributes"`
	TemporaryPassword      string          `json:"TemporaryPassword"`
	MessageAction          string          `json:"MessageAction"`
	DesiredDeliveryMediums []string        `json:"DesiredDeliveryMediums"`
	ForceAliasCreation     bool            `json:"ForceAliasCreation"`
}

type adminCreateUserResponse struct {
	User userTypeJSON `json:"User"`
}

func (h *Handler) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *adminCreateUserRequest) (any, error) {
		u, err := h.cognito.AdminCreateUser(ctx, driver.AdminCreateUserInput{
			UserPoolID:             req.UserPoolID,
			Username:               req.Username,
			UserAttributes:         attributesFromWire(req.UserAttributes),
			TemporaryPassword:      req.TemporaryPassword,
			MessageAction:          req.MessageAction,
			DesiredDeliveryMediums: req.DesiredDeliveryMediums,
			ForceAliasCreation:     req.ForceAliasCreation,
		})
		if err != nil {
			return nil, err
		}

		return adminCreateUserResponse{User: userToWire(u)}, nil
	})
}

// userRef is the {UserPoolId, Username} pair most admin user operations take.
type userRef struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
}

func (h *Handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *userRef) (any, error) {
		u, err := h.cognito.AdminGetUser(ctx, req.UserPoolID, req.Username)
		if err != nil {
			return nil, err
		}

		return adminGetUserResponse{
			Username:             u.Username,
			UserAttributes:       attributesToWire(u.Attributes),
			UserCreateDate:       epochOrNil(u.UserCreateDate),
			UserLastModifiedDate: epochOrNil(u.UserLastModifiedDate),
			Enabled:              u.Enabled,
			UserStatus:           u.UserStatus,
		}, nil
	})
}

type listUsersRequest struct {
	UserPoolID      string   `json:"UserPoolId"`
	AttributesToGet []string `json:"AttributesToGet"`
	Limit           int32    `json:"Limit"`
	PaginationToken string   `json:"PaginationToken"`
	Filter          string   `json:"Filter"`
}

type listUsersResponse struct {
	Users           []userTypeJSON `json:"Users"`
	PaginationToken string         `json:"PaginationToken,omitempty"`
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listUsersRequest) (any, error) {
		users, next, err := h.cognito.ListUsers(ctx, driver.ListUsersInput{
			UserPoolID:      req.UserPoolID,
			Filter:          req.Filter,
			AttributesToGet: req.AttributesToGet,
			Limit:           req.Limit,
			PaginationToken: req.PaginationToken,
		})
		if err != nil {
			return nil, err
		}

		out := make([]userTypeJSON, len(users))
		for i := range users {
			out[i] = userToWire(&users[i])
		}

		return listUsersResponse{Users: out, PaginationToken: next}, nil
	})
}

func (h *Handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	h.simpleUserOp(w, r, h.cognito.AdminDeleteUser)
}

func (h *Handler) adminEnableUser(w http.ResponseWriter, r *http.Request) {
	h.simpleUserOp(w, r, h.cognito.AdminEnableUser)
}

func (h *Handler) adminDisableUser(w http.ResponseWriter, r *http.Request) {
	h.simpleUserOp(w, r, h.cognito.AdminDisableUser)
}

func (h *Handler) adminResetUserPassword(w http.ResponseWriter, r *http.Request) {
	h.simpleUserOp(w, r, h.cognito.AdminResetUserPassword)
}

// simpleUserOp serves an operation that takes only {UserPoolId, Username} and
// returns an empty body.
func (h *Handler) simpleUserOp(w http.ResponseWriter, r *http.Request, call func(context.Context, string, string) error) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *userRef) (any, error) {
		if err := call(ctx, req.UserPoolID, req.Username); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type adminUpdateUserAttributesRequest struct {
	UserPoolID     string          `json:"UserPoolId"`
	Username       string          `json:"Username"`
	UserAttributes []attributeJSON `json:"UserAttributes"`
}

func (h *Handler) adminUpdateUserAttributes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *adminUpdateUserAttributesRequest) (any, error) {
		err := h.cognito.AdminUpdateUserAttributes(ctx, req.UserPoolID, req.Username, attributesFromWire(req.UserAttributes))
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type adminDeleteUserAttributesRequest struct {
	UserPoolID         string   `json:"UserPoolId"`
	Username           string   `json:"Username"`
	UserAttributeNames []string `json:"UserAttributeNames"`
}

func (h *Handler) adminDeleteUserAttributes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *adminDeleteUserAttributesRequest) (any, error) {
		if err := h.cognito.AdminDeleteUserAttributes(ctx, req.UserPoolID, req.Username, req.UserAttributeNames); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type adminSetUserPasswordRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
	Password   string `json:"Password"`
	Permanent  bool   `json:"Permanent"`
}

func (h *Handler) adminSetUserPassword(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *adminSetUserPasswordRequest) (any, error) {
		if err := h.cognito.AdminSetUserPassword(ctx, req.UserPoolID, req.Username, req.Password, req.Permanent); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type addCustomAttributesRequest struct {
	UserPoolID       string                `json:"UserPoolId"`
	CustomAttributes []schemaAttributeJSON `json:"CustomAttributes"`
}

func (h *Handler) addCustomAttributes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *addCustomAttributesRequest) (any, error) {
		if err := h.cognito.AddCustomAttributes(ctx, req.UserPoolID, schemaAttributesFromWire(req.CustomAttributes)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

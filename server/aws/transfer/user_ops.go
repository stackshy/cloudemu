package transfer

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

type createUserRequest struct {
	ServerID              string                `json:"ServerId"`
	UserName              string                `json:"UserName"`
	Role                  string                `json:"Role"`
	HomeDirectory         string                `json:"HomeDirectory"`
	HomeDirectoryType     string                `json:"HomeDirectoryType"`
	HomeDirectoryMappings []homeDirMapEntryJSON `json:"HomeDirectoryMappings"`
	Policy                string                `json:"Policy"`
	PosixProfile          *posixProfileJSON     `json:"PosixProfile"`
	SSHPublicKeyBody      string                `json:"SshPublicKeyBody"`
	Tags                  []tagJSON             `json:"Tags"`
}

type createUserResponse struct {
	ServerID string `json:"ServerId"`
	UserName string `json:"UserName"`
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createUserRequest) (any, error) {
		u := driver.User{
			ServerID:              req.ServerID,
			UserName:              req.UserName,
			Role:                  req.Role,
			HomeDirectory:         req.HomeDirectory,
			HomeDirectoryType:     req.HomeDirectoryType,
			HomeDirectoryMappings: homeDirMappingsFromWire(req.HomeDirectoryMappings),
			Policy:                req.Policy,
			PosixProfile:          posixProfileFromWire(req.PosixProfile),
			Tags:                  tagsToMap(req.Tags),
		}
		if err := h.transfer.CreateUser(ctx, u); err != nil {
			return nil, err
		}

		// An SSHPublicKeyBody supplied at create time is imported as the user's
		// first key, matching real Transfer.
		if req.SSHPublicKeyBody != "" {
			if _, err := h.transfer.ImportSSHPublicKey(ctx, req.ServerID, req.UserName, req.SSHPublicKeyBody); err != nil {
				return nil, err
			}
		}

		return createUserResponse{ServerID: req.ServerID, UserName: req.UserName}, nil
	})
}

type describeUserRequest struct {
	ServerID string `json:"ServerId"`
	UserName string `json:"UserName"`
}

type describeUserResponse struct {
	ServerID string            `json:"ServerId"`
	User     describedUserJSON `json:"User"`
}

func (h *Handler) describeUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeUserRequest) (any, error) {
		u, err := h.transfer.DescribeUser(ctx, req.ServerID, req.UserName)
		if err != nil {
			return nil, err
		}

		return describeUserResponse{ServerID: req.ServerID, User: userToWire(u)}, nil
	})
}

type updateUserRequest struct {
	ServerID              string                `json:"ServerId"`
	UserName              string                `json:"UserName"`
	Role                  *string               `json:"Role"`
	HomeDirectory         *string               `json:"HomeDirectory"`
	HomeDirectoryType     *string               `json:"HomeDirectoryType"`
	HomeDirectoryMappings []homeDirMapEntryJSON `json:"HomeDirectoryMappings"`
	Policy                *string               `json:"Policy"`
	PosixProfile          *posixProfileJSON     `json:"PosixProfile"`
}

type updateUserResponse struct {
	ServerID string `json:"ServerId"`
	UserName string `json:"UserName"`
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateUserRequest) (any, error) {
		upd := driver.UserUpdate{
			Role:                  req.Role,
			HomeDirectory:         req.HomeDirectory,
			HomeDirectoryType:     req.HomeDirectoryType,
			HomeDirectoryMappings: homeDirMappingsFromWire(req.HomeDirectoryMappings),
			Policy:                req.Policy,
			PosixProfile:          posixProfileFromWire(req.PosixProfile),
		}
		if err := h.transfer.UpdateUser(ctx, req.ServerID, req.UserName, upd); err != nil {
			return nil, err
		}

		return updateUserResponse{ServerID: req.ServerID, UserName: req.UserName}, nil
	})
}

type deleteUserRequest struct {
	ServerID string `json:"ServerId"`
	UserName string `json:"UserName"`
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteUserRequest) (any, error) {
		if err := h.transfer.DeleteUser(ctx, req.ServerID, req.UserName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listUsersRequest struct {
	ServerID   string `json:"ServerId"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listUsersResponse struct {
	ServerID  string           `json:"ServerId"`
	Users     []listedUserJSON `json:"Users"`
	NextToken string           `json:"NextToken,omitempty"`
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listUsersRequest) (any, error) {
		users, next, err := h.transfer.ListUsers(ctx, req.ServerID,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]listedUserJSON, 0, len(users))
		for i := range users {
			out = append(out, listedUserToWire(&users[i]))
		}

		return listUsersResponse{ServerID: req.ServerID, Users: out, NextToken: next}, nil
	})
}

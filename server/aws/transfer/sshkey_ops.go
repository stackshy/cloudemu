package transfer

import (
	"context"
	"net/http"
)

type importSSHPublicKeyRequest struct {
	ServerID         string `json:"ServerId"`
	UserName         string `json:"UserName"`
	SSHPublicKeyBody string `json:"SshPublicKeyBody"`
}

type importSSHPublicKeyResponse struct {
	ServerID       string `json:"ServerId"`
	SSHPublicKeyID string `json:"SshPublicKeyId"`
	UserName       string `json:"UserName"`
}

func (h *Handler) importSSHPublicKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *importSSHPublicKeyRequest) (any, error) {
		keyID, err := h.transfer.ImportSSHPublicKey(ctx, req.ServerID, req.UserName, req.SSHPublicKeyBody)
		if err != nil {
			return nil, err
		}

		return importSSHPublicKeyResponse{ServerID: req.ServerID, SSHPublicKeyID: keyID, UserName: req.UserName}, nil
	})
}

type deleteSSHPublicKeyRequest struct {
	ServerID       string `json:"ServerId"`
	SSHPublicKeyID string `json:"SshPublicKeyId"`
	UserName       string `json:"UserName"`
}

func (h *Handler) deleteSSHPublicKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteSSHPublicKeyRequest) (any, error) {
		if err := h.transfer.DeleteSSHPublicKey(ctx, req.ServerID, req.UserName, req.SSHPublicKeyID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

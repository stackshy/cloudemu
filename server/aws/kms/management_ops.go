package kms

import (
	"context"
	"net/http"
	"sort"

	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func (h *Handler) createGrant(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createGrantRequest) (any, error) {
		grantID, token, err := h.kms.CreateGrant(ctx, kmsdriver.CreateGrantInput{
			KeyID:             req.KeyID,
			GranteePrincipal:  req.GranteePrincipal,
			RetiringPrincipal: req.RetiringPrincipal,
			Name:              req.Name,
			Operations:        req.Operations,
			Constraints:       toDriverConstraints(req.Constraints),
		})
		if err != nil {
			return nil, err
		}

		return createGrantResponse{GrantID: grantID, GrantToken: token}, nil
	})
}

//nolint:dupl // templated KMS wire handler; the decode/paginate/respond shape is intrinsic
func (h *Handler) listGrants(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listGrantsRequest) (any, error) {
		grants, err := h.kms.ListGrants(ctx, req.KeyID)
		if err != nil {
			return nil, err
		}

		sort.Slice(grants, func(i, j int) bool { return grants[i].GrantID < grants[j].GrantID })

		start, end, next, truncated, perr := pageWindow(req.Marker, req.Limit, len(grants))
		if perr != nil {
			return nil, perr
		}

		out := make([]grantJSON, 0, end-start)
		for i := start; i < end; i++ {
			out = append(out, grantToJSON(&grants[i]))
		}

		return listGrantsResponse{Grants: out, NextMarker: next, Truncated: truncated}, nil
	})
}

func (h *Handler) revokeGrant(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *revokeGrantRequest) (any, error) {
		if err := h.kms.RevokeGrant(ctx, req.KeyID, req.GrantID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) retireGrant(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *retireGrantRequest) (any, error) {
		if err := h.kms.RetireGrant(ctx, req.GrantToken, req.KeyID, req.GrantID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

//nolint:dupl // templated KMS wire handler; the decode/paginate/respond shape is intrinsic
func (h *Handler) listRetirableGrants(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listRetirableGrantsRequest) (any, error) {
		grants, err := h.kms.ListRetirableGrants(ctx, req.RetiringPrincipal)
		if err != nil {
			return nil, err
		}

		sort.Slice(grants, func(i, j int) bool { return grants[i].GrantID < grants[j].GrantID })

		start, end, next, truncated, perr := pageWindow(req.Marker, req.Limit, len(grants))
		if perr != nil {
			return nil, perr
		}

		out := make([]grantJSON, 0, end-start)
		for i := start; i < end; i++ {
			out = append(out, grantToJSON(&grants[i]))
		}

		return listGrantsResponse{Grants: out, NextMarker: next, Truncated: truncated}, nil
	})
}

func (h *Handler) enableKeyRotation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *enableKeyRotationRequest) (any, error) {
		if err := h.kms.EnableKeyRotation(ctx, req.KeyID, req.RotationPeriodInDays); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) disableKeyRotation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *keyIDRequest) (any, error) {
		if err := h.kms.DisableKeyRotation(ctx, req.KeyID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) getKeyRotationStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *keyIDRequest) (any, error) {
		st, err := h.kms.GetKeyRotationStatus(ctx, req.KeyID)
		if err != nil {
			return nil, err
		}

		return getKeyRotationStatusResponse{
			KeyID:                 st.KeyID,
			KeyRotationEnabled:    st.Enabled,
			RotationPeriodInDays:  st.RotationPeriodDays,
			NextRotationDate:      epochOrNil(st.NextRotationDate),
			OnDemandRotationCount: st.OnDemandRotationCount,
		}, nil
	})
}

func (h *Handler) listKeyRotations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listKeyRotationsRequest) (any, error) {
		events, err := h.kms.ListKeyRotations(ctx, req.KeyID)
		if err != nil {
			return nil, err
		}

		start, end, next, truncated, perr := pageWindow(req.Marker, req.Limit, len(events))
		if perr != nil {
			return nil, perr
		}

		out := make([]rotationJSON, 0, end-start)
		for i := start; i < end; i++ {
			out = append(out, rotationJSON{
				KeyID:        events[i].KeyID,
				RotationDate: epochOrNil(events[i].RotationDate),
				RotationType: events[i].RotationType,
			})
		}

		return listKeyRotationsResponse{Rotations: out, NextMarker: next, Truncated: truncated}, nil
	})
}

func (h *Handler) rotateKeyOnDemand(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *keyIDRequest) (any, error) {
		if err := h.kms.RotateKeyOnDemand(ctx, req.KeyID); err != nil {
			return nil, err
		}

		return rotateKeyOnDemandResponse{KeyID: req.KeyID}, nil
	})
}

func (h *Handler) getKeyPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getKeyPolicyRequest) (any, error) {
		doc, err := h.kms.GetKeyPolicy(ctx, req.KeyID, req.PolicyName)
		if err != nil {
			return nil, err
		}

		name := req.PolicyName
		if name == "" {
			name = kmsdriver.DefaultPolicyName
		}

		return getKeyPolicyResponse{Policy: doc, PolicyName: name}, nil
	})
}

func (h *Handler) putKeyPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putKeyPolicyRequest) (any, error) {
		if err := h.kms.PutKeyPolicy(ctx, req.KeyID, req.PolicyName, req.Policy); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listKeyPolicies(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listKeyPoliciesRequest) (any, error) {
		names, err := h.kms.ListKeyPolicies(ctx, req.KeyID)
		if err != nil {
			return nil, err
		}

		sort.Strings(names)

		start, end, next, truncated, perr := pageWindow(req.Marker, req.Limit, len(names))
		if perr != nil {
			return nil, perr
		}

		return listKeyPoliciesResponse{PolicyNames: names[start:end], NextMarker: next, Truncated: truncated}, nil
	})
}

package identity

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// principalOps binds one of the two identical collections — users and groups —
// so both are routed and served by the same code.
type principalOps struct {
	kind   string
	create func(context.Context, iamdriver.PrincipalSpec) (*iamdriver.PrincipalInfo, error)
	get    func(context.Context, string) (*iamdriver.PrincipalInfo, error)
	list   func(context.Context, string) ([]iamdriver.PrincipalInfo, error)
	update func(context.Context, string, iamdriver.IdentityUpdate) (*iamdriver.PrincipalInfo, error)
	remove func(context.Context, string) error
}

// opsFor binds the driver methods for one principal collection.
func (h *Handler) opsFor(kind string) principalOps {
	if kind == kindGroup {
		return principalOps{
			kind:   kindGroup,
			create: h.identity.CreateOCIGroup,
			get:    h.identity.GetOCIGroup,
			list:   h.identity.ListOCIGroups,
			update: h.identity.UpdateOCIGroup,
			remove: h.identity.DeleteOCIGroup,
		}
	}

	return principalOps{
		kind:   kindUser,
		create: h.identity.CreateOCIUser,
		get:    h.identity.GetOCIUser,
		list:   h.identity.ListOCIUsers,
		update: h.identity.UpdateOCIUser,
		remove: h.identity.DeleteOCIUser,
	}
}

// routePrincipal dispatches the /users and /groups surfaces.
func (h *Handler) routePrincipal(w http.ResponseWriter, r *http.Request, kind, id string) {
	if h.identity == nil {
		capabilityMissing(w, r, kind)
		return
	}

	o := h.opsFor(kind)
	ops := collectionOps{
		kind:   kind,
		create: o.serveCreate,
		list:   o.serveList,
		get:    o.serveGet,
		update: o.serveUpdate,
		remove: o.serveDelete,
	}

	ops.route(w, r, id)
}

func (o *principalOps) serveCreate(w http.ResponseWriter, r *http.Request) {
	var body createPrincipalBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	if !requireBodyCompartment(w, r, body.CompartmentID) {
		return
	}

	info, err := o.create(r.Context(), iamdriver.PrincipalSpec{
		CompartmentID: body.CompartmentID,
		Name:          body.Name,
		Description:   body.Description,
		FreeformTags:  body.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPrincipalResource(info))
}

func (o *principalOps) serveList(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := o.list(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]principalResource, 0, len(infos))
	for i := range infos {
		out = append(out, toPrincipalResource(&infos[i]))
	}

	writeList(w, r, out)
}

func (o *principalOps) serveGet(w http.ResponseWriter, r *http.Request, id string) {
	info, err := o.get(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPrincipalResource(info))
}

func (o *principalOps) serveUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var body updateBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	info, err := o.update(r.Context(), id, body.identityUpdate())
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPrincipalResource(info))
}

func (o *principalOps) serveDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := o.remove(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

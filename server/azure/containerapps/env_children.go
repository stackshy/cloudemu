package containerapps

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/containerapps"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	subResourceDapr     = "daprComponents"
	subResourceStorages = "storages"
	actionListSecrets   = "listSecrets"

	// Deepest paths after {type}: …/daprComponents/{n}/listSecrets and
	// …/storages/{n}.
	daprActionDepth = 4
	envStorageDepth = 3
)

type envChildResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Properties any    `json:"properties"`
}

func envChildResp(rp *azurearm.ResourcePath, child, name string, props any) envChildResponse {
	return envChildResponse{
		ID: azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeEnvironments, rp.ResourceName) +
			"/" + child + "/" + name,
		Name:       name,
		Type:       providerName + "/" + typeEnvironments + "/" + child,
		Properties: props,
	}
}

// envChildOps binds one environment child type to its store calls and wire
// shape, so both child types share one PUT/GET/DELETE/list router.
type envChildOps[T any] struct {
	put    func(in *T) (T, error)
	get    func() (T, error)
	del    func() bool
	list   func() ([]T, error)
	render func(v *T) envChildResponse
	// setName stamps the URL name onto a decoded PUT body.
	setName func(v *T)
}

// serveEnvChild routes …/{child} (list) and …/{child}/{name}.
func serveEnvChild[T any](w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, ops envChildOps[T]) {
	if rp.SubResourceName == "" {
		serveEnvChildList(w, r, ops)
		return
	}

	var (
		v   T
		err error
	)

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Properties T `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &req) {
			return
		}

		ops.setName(&req.Properties)

		if v, err = ops.put(&req.Properties); err != nil {
			// A missing environment is ParentResourceNotFound, as in real ARM.
			if cerrors.IsNotFound(err) {
				azurearm.WriteParentNotFound(w, err)
				return
			}

			azurearm.WriteCErr(w, err)

			return
		}
	case http.MethodGet:
		if v, err = ops.get(); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	case http.MethodDelete:
		writeDeleteStatus(w, ops.del())
		return
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, ops.render(&v))
}

func serveEnvChildList[T any](w http.ResponseWriter, r *http.Request, ops envChildOps[T]) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := ops.list()
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listEnvelope[envChildResponse]{Value: make([]envChildResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, ops.render(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// serveDapr serves managedEnvironments/{e}/daprComponents[/{n}[/listSecrets]].
// GET returns secret names only; values are read with POST …/listSecrets.
func (h *Handler) serveDapr(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, daprActionDepth) {
		return
	}

	ctx, sub, rg, env, name := r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName

	if rp.SubResourceAction != "" {
		if !strings.EqualFold(rp.SubResourceAction, actionListSecrets) || r.Method != http.MethodPost {
			azurearm.WriteUnknownType(w, r, rp)
			return
		}

		c, err := h.store.GetDaprComponent(ctx, sub, rg, env, name)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": c.Secrets})

		return
	}

	serveEnvChild(w, r, rp, envChildOps[containerapps.DaprComponent]{
		put: func(in *containerapps.DaprComponent) (containerapps.DaprComponent, error) {
			return h.store.PutDaprComponent(ctx, sub, rg, env, in)
		},
		get: func() (containerapps.DaprComponent, error) { return h.store.GetDaprComponent(ctx, sub, rg, env, name) },
		del: func() bool {
			existed, _ := h.store.DeleteDaprComponent(ctx, sub, rg, env, name)
			return existed
		},
		list: func() ([]containerapps.DaprComponent, error) { return h.store.ListDaprComponents(ctx, sub, rg, env) },
		render: func(c *containerapps.DaprComponent) envChildResponse {
			masked := *c
			masked.Secrets = make([]containerapps.DaprSecret, len(c.Secrets))

			for i, s := range c.Secrets {
				masked.Secrets[i] = containerapps.DaprSecret{Name: s.Name, KeyVaultURL: s.KeyVaultURL, Identity: s.Identity}
			}

			return envChildResp(rp, subResourceDapr, c.Name, masked)
		},
		setName: func(c *containerapps.DaprComponent) { c.Name = name },
	})
}

// serveEnvStorage serves managedEnvironments/{e}/storages[/{n}]. The account
// key is write-only and never returned.
func (h *Handler) serveEnvStorage(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if azurearm.TooDeep(w, r, rp, envStorageDepth) {
		return
	}

	ctx, sub, rg, env, name := r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName

	serveEnvChild(w, r, rp, envChildOps[containerapps.EnvStorage]{
		put: func(in *containerapps.EnvStorage) (containerapps.EnvStorage, error) {
			return h.store.PutEnvStorage(ctx, sub, rg, env, in)
		},
		get: func() (containerapps.EnvStorage, error) { return h.store.GetEnvStorage(ctx, sub, rg, env, name) },
		del: func() bool {
			existed, _ := h.store.DeleteEnvStorage(ctx, sub, rg, env, name)
			return existed
		},
		list: func() ([]containerapps.EnvStorage, error) { return h.store.ListEnvStorages(ctx, sub, rg, env) },
		render: func(s *containerapps.EnvStorage) envChildResponse {
			out := containerapps.EnvStorage{Name: s.Name}
			if s.AzureFile != nil {
				f := *s.AzureFile
				f.AccountKey = ""
				out.AzureFile = &f
			}

			return envChildResp(rp, subResourceStorages, s.Name, out)
		},
		setName: func(s *containerapps.EnvStorage) { s.Name = name },
	})
}

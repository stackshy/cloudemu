package azuresearch

import (
	"net/http"

	srchdriver "github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// actionMethods is the HTTP method each key action requires, matching the real
// Microsoft.Search REST contract.
//
//nolint:gochecknoglobals // immutable routing set
var actionMethods = map[string]string{
	subAdminKeys:   http.MethodPost,
	subRegenerate:  http.MethodPost,
	subListQuery:   http.MethodPost,
	subCreateQuery: http.MethodPost,
	subDeleteQuery: http.MethodDelete,
}

// serveServiceAction handles the key verbs under a service.
func (h *ControlHandler) serveServiceAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, name := rp.ResourceGroup, rp.ResourceName

	if want, ok := actionMethods[rp.SubResource]; ok && r.Method != want {
		writeMethodNotAllowed(w)

		return
	}

	switch rp.SubResource {
	case subAdminKeys:
		keys, err := h.svc.ListAdminKeys(r.Context(), rg, name)
		writeAdminKeys(w, keys, err)
	case subRegenerate:
		// .../regenerateAdminKey/{primary|secondary}
		keys, err := h.svc.RegenerateAdminKey(r.Context(), rg, name, rp.SubResourceName)
		writeAdminKeys(w, keys, err)
	case subListQuery:
		keys, err := h.svc.ListQueryKeys(r.Context(), rg, name)
		writeQueryKeys(w, keys, err)
	case subCreateQuery:
		// .../createQueryKey/{keyName}
		qk, err := h.svc.CreateQueryKey(r.Context(), rg, name, rp.SubResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, queryKeyJSON(qk))
	case subDeleteQuery:
		// .../deleteQueryKey/{key}
		if err := h.svc.DeleteQueryKey(r.Context(), rg, name, rp.SubResourceName); err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown action: "+rp.SubResource)
	}
}

func writeAdminKeys(w http.ResponseWriter, keys *srchdriver.AdminKeys, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"primaryKey": keys.Primary, "secondaryKey": keys.Secondary})
}

func queryKeyJSON(qk *srchdriver.QueryKey) map[string]any {
	return map[string]any{"name": qk.Name, "key": qk.Key}
}

func writeQueryKeys(w http.ResponseWriter, keys []srchdriver.QueryKey, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(keys))
	for i := range keys {
		out = append(out, queryKeyJSON(&keys[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}

func sharedLinkJSON(l *srchdriver.SharedPrivateLink) map[string]any {
	return map[string]any{
		"id": l.ID, "name": l.Name, "type": searchProvider + "/searchServices/sharedPrivateLinkResources",
		"properties": map[string]any{
			"groupId": l.GroupID, "privateLinkResourceId": l.PrivateLinkID,
			"status": l.Status, "provisioningState": l.ProvisioningState,
		},
	}
}

func (h *ControlHandler) serveSharedLink(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, name := rp.ResourceGroup, rp.ResourceName

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		links, err := h.svc.ListSharedPrivateLinks(r.Context(), rg, name)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(links))
		for i := range links {
			out = append(out, sharedLinkJSON(&links[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	link := rp.SubResourceName

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				GroupID               string `json:"groupId"`
				PrivateLinkResourceID string `json:"privateLinkResourceId"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		l, err := h.svc.PutSharedPrivateLink(r.Context(), rg, name, link, body.Properties.GroupID, body.Properties.PrivateLinkResourceID)
		writeRes(w, sharedLinkJSON, l, err)
	case http.MethodGet:
		l, err := h.svc.GetSharedPrivateLink(r.Context(), rg, name, link)
		writeRes(w, sharedLinkJSON, l, err)
	case http.MethodDelete:
		if err := h.svc.DeleteSharedPrivateLink(r.Context(), rg, name, link); err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
	default:
		writeMethodNotAllowed(w)
	}
}

func pecJSON(c *srchdriver.PrivateEndpointConnection) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "type": searchProvider + "/searchServices/privateEndpointConnections",
		"properties": map[string]any{
			"provisioningState": c.ProvisioningState,
			"privateLinkServiceConnectionState": map[string]any{
				"status": c.Status, "description": c.Description,
			},
		},
	}
}

func (h *ControlHandler) servePEC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rg, name := rp.ResourceGroup, rp.ResourceName

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		conns, err := h.svc.ListPrivateEndpointConnections(r.Context(), rg, name)
		if err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(conns))
		for i := range conns {
			out = append(out, pecJSON(&conns[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	conn := rp.SubResourceName

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				State struct {
					Status string `json:"status"`
				} `json:"privateLinkServiceConnectionState"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		c, err := h.svc.PutPrivateEndpointConnection(r.Context(), rg, name, conn, body.Properties.State.Status)
		writeRes(w, pecJSON, c, err)
	case http.MethodGet:
		c, err := h.svc.GetPrivateEndpointConnection(r.Context(), rg, name, conn)
		writeRes(w, pecJSON, c, err)
	case http.MethodDelete:
		if err := h.svc.DeletePrivateEndpointConnection(r.Context(), rg, name, conn); err != nil {
			azurearm.WriteCErr(w, err)

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{})
	default:
		writeMethodNotAllowed(w)
	}
}

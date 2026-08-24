package notificationhubs

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const pnsCredentialsType = "Microsoft.NotificationHubs/namespaces/notificationHubs/pnsCredentials"

// pnsCredentialKeys are the platform credential fields Notification Hubs carries
// in a hub's properties and echoes from GetPnsCredentials.
var pnsCredentialKeys = []string{ //nolint:gochecknoglobals // static lookup table
	"admCredential",
	"apnsCredential",
	"baiduCredential",
	"gcmCredential",
	"mpnsCredential",
	"wnsCredential",
}

// hubPutBody decodes a hub CreateOrUpdate request, keeping the properties block
// raw so platform-specific PNS credentials survive verbatim.
type hubPutBody struct {
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties json.RawMessage   `json:"properties,omitempty"`
}

// registrationTTL extracts the registrationTtl from the raw properties block.
func (b *hubPutBody) registrationTTL() string {
	if len(b.Properties) == 0 {
		return ""
	}

	var props struct {
		RegistrationTTL string `json:"registrationTtl"`
	}

	_ = json.Unmarshal(b.Properties, &props)

	return props.RegistrationTTL
}

// pnsCredentialsResource is the GetPnsCredentials response
// (armnotificationhubs.PnsCredentialsResource). Properties carries only the
// platform credential objects, each in its original wire shape.
type pnsCredentialsResource struct {
	ID         string                     `json:"id,omitempty"`
	Name       string                     `json:"name,omitempty"`
	Type       string                     `json:"type,omitempty"`
	Location   string                     `json:"location,omitempty"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// storePnsCredentials persists the credential-bearing properties supplied at hub
// create/update. It stores nothing when the driver lacks the Azure capability or
// the request carried no credentials, so a hub without credentials reports none.
func (h *Handler) storePnsCredentials(r *http.Request, hubKey string, rawProps json.RawMessage) {
	az, ok := h.az()
	if !ok || len(rawProps) == 0 {
		return
	}

	if len(extractPnsCredentials(rawProps)) == 0 {
		return
	}

	_ = az.SetPnsCredentials(r.Context(), hubKey, string(rawProps))
}

// extractPnsCredentials returns the platform credential fields present in a raw
// properties block, keyed by their wire name.
func extractPnsCredentials(rawProps json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if len(rawProps) == 0 {
		return out
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(rawProps, &props); err != nil {
		return out
	}

	for _, k := range pnsCredentialKeys {
		if v, present := props[k]; present {
			out[k] = v
		}
	}

	return out
}

// getPnsCredentials serves NotificationHubs.GetPnsCredentials, echoing the PNS
// credentials captured when the hub was created or updated.
func (h *Handler) getPnsCredentials(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, hub string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	key := hubKey(rp.ResourceName, hub)

	info, err := h.notif.GetTopic(r.Context(), key)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	stored, err := az.GetPnsCredentials(r.Context(), key)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNamespaces, rp.ResourceName) +
		"/" + subHubs + "/" + hub + "/pnsCredentials"

	azurearm.WriteJSON(w, http.StatusOK, pnsCredentialsResource{
		ID:         id,
		Name:       hub,
		Type:       pnsCredentialsType,
		Location:   nsLocation(info),
		Properties: extractPnsCredentials(json.RawMessage(stored)),
	})
}

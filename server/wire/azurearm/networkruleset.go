package azurearm

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// NetworkRuleSetName is the only name a Service Bus or Event Hubs namespace
// network rule set can have.
const NetworkRuleSetName = "default"

const (
	networkRuleSetMaxBody = 1 << 20
	ruleActionAllow       = "Allow"
	listValueKey          = "value"
)

// NetworkRuleSetProps is the stored properties of a Service Bus or Event Hubs
// namespace networkRuleSets/default singleton.
type NetworkRuleSetProps struct {
	DefaultAction               string            `json:"defaultAction"`
	PublicNetworkAccess         string            `json:"publicNetworkAccess"`
	TrustedServiceAccessEnabled bool              `json:"trustedServiceAccessEnabled"`
	VirtualNetworkRules         []json.RawMessage `json:"virtualNetworkRules"`
	IPRules                     []NetworkIPRule   `json:"ipRules"`
}

// NetworkIPRule is one ipRules entry of a namespace network rule set.
type NetworkIPRule struct {
	IPMask string `json:"ipMask"`
	Action string `json:"action"`
}

type networkRuleSetResource struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location,omitempty"`
	Properties NetworkRuleSetProps `json:"properties"`
}

type networkRuleSetRequest struct {
	Properties struct {
		DefaultAction               *string           `json:"defaultAction"`
		PublicNetworkAccess         *string           `json:"publicNetworkAccess"`
		TrustedServiceAccessEnabled *bool             `json:"trustedServiceAccessEnabled"`
		VirtualNetworkRules         []json.RawMessage `json:"virtualNetworkRules"`
		IPRules                     []NetworkIPRule   `json:"ipRules"`
	} `json:"properties"`
}

// DefaultNetworkRuleSet is what a namespace reports before its rule set is
// ever written: allow everything, no rules.
func DefaultNetworkRuleSet() NetworkRuleSetProps {
	return NetworkRuleSetProps{
		DefaultAction:       ruleActionAllow,
		PublicNetworkAccess: "Enabled",
		VirtualNetworkRules: []json.RawMessage{},
		IPRules:             []NetworkIPRule{},
	}
}

// CloneNetworkRuleSet deep-copies p, returning the defaults when p is nil.
func CloneNetworkRuleSet(p *NetworkRuleSetProps) NetworkRuleSetProps {
	if p == nil {
		return DefaultNetworkRuleSet()
	}

	out := *p
	out.IPRules = append([]NetworkIPRule{}, p.IPRules...)
	out.VirtualNetworkRules = make([]json.RawMessage, len(p.VirtualNetworkRules))

	for i, v := range p.VirtualNetworkRules {
		out.VirtualNetworkRules[i] = append(json.RawMessage(nil), v...)
	}

	return out
}

// DecodeNetworkRuleSet reads a CreateOrUpdateNetworkRuleSet body. Absent keys
// take the defaults, never previously stored values, as real Azure replaces
// the whole rule set. On a validation failure it writes the 400 and returns
// false.
func DecodeNetworkRuleSet(w http.ResponseWriter, r *http.Request) (*NetworkRuleSetProps, bool) {
	var req networkRuleSetRequest

	r.Body = http.MaxBytesReader(w, r.Body, networkRuleSetMaxBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return nil, false
	}

	out := DefaultNetworkRuleSet()
	in := req.Properties

	if in.DefaultAction != nil {
		out.DefaultAction = *in.DefaultAction
	}

	if in.PublicNetworkAccess != nil {
		out.PublicNetworkAccess = *in.PublicNetworkAccess
	}

	if in.TrustedServiceAccessEnabled != nil {
		out.TrustedServiceAccessEnabled = *in.TrustedServiceAccessEnabled
	}

	if in.VirtualNetworkRules != nil {
		out.VirtualNetworkRules = in.VirtualNetworkRules
	}

	if in.IPRules != nil {
		out.IPRules = in.IPRules
	}

	if msg := validateNetworkRuleSet(&out); msg != "" {
		WriteError(w, http.StatusBadRequest, "BadRequest", msg)
		return nil, false
	}

	return &out, true
}

func validateNetworkRuleSet(p *NetworkRuleSetProps) string {
	if !oneOf(p.DefaultAction, ruleActionAllow, "Deny") {
		return "invalid defaultAction: " + p.DefaultAction
	}

	if !oneOf(p.PublicNetworkAccess, "Enabled", "Disabled", "SecuredByPerimeter") {
		return "invalid publicNetworkAccess: " + p.PublicNetworkAccess
	}

	for i := range p.IPRules {
		if p.IPRules[i].Action == "" {
			p.IPRules[i].Action = ruleActionAllow
		}

		if !oneOf(p.IPRules[i].Action, ruleActionAllow) {
			return "invalid ipRules action: " + p.IPRules[i].Action
		}

		if p.IPRules[i].IPMask == "" {
			return "ipRules ipMask is required"
		}
	}

	return ""
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if strings.EqualFold(v, a) {
			return true
		}
	}

	return false
}

// NetworkRuleSetStore binds the shared networkRuleSets routes to one
// namespace. Load and Store run the handler's own locking and report ok=false
// when the namespace does not exist.
type NetworkRuleSetStore struct {
	// Type is the ARM resource type, e.g. "Microsoft.ServiceBus/Namespaces/NetworkRuleSets".
	Type     string
	Load     func() (nsID, location string, p NetworkRuleSetProps, ok bool)
	Store    func(p *NetworkRuleSetProps) (nsID, location string, ok bool)
	NotFound func(w http.ResponseWriter)
}

// ServeNetworkRuleSet serves .../networkRuleSets[/default]. rest is the path
// after "networkRuleSets". The singleton always exists while its namespace
// does, so GET never 404s for "default"; there is no DELETE or PATCH.
func ServeNetworkRuleSet(w http.ResponseWriter, r *http.Request, rest []string, st NetworkRuleSetStore) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		nsID, loc, p, ok := st.Load()
		if !ok {
			st.NotFound(w)
			return
		}

		WriteJSON(w, http.StatusOK, map[string]any{listValueKey: []networkRuleSetResource{
			networkRuleSetBody(nsID, st.Type, loc, &p),
		}})
	case len(rest) == 0:
		WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	case len(rest) > 1 || !strings.EqualFold(rest[0], NetworkRuleSetName):
		WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"network rule set not found: "+strings.Join(rest, "/"))
	case r.Method == http.MethodGet:
		nsID, loc, p, ok := st.Load()
		if !ok {
			st.NotFound(w)
			return
		}

		WriteJSON(w, http.StatusOK, networkRuleSetBody(nsID, st.Type, loc, &p))
	case r.Method == http.MethodPut:
		putNetworkRuleSet(w, r, st)
	default:
		WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func putNetworkRuleSet(w http.ResponseWriter, r *http.Request, st NetworkRuleSetStore) {
	p, ok := DecodeNetworkRuleSet(w, r)
	if !ok {
		return
	}

	out := CloneNetworkRuleSet(p)

	nsID, loc, ok := st.Store(p)
	if !ok {
		st.NotFound(w)
		return
	}

	WriteJSON(w, http.StatusOK, networkRuleSetBody(nsID, st.Type, loc, &out))
}

func networkRuleSetBody(nsID, typ, location string, p *NetworkRuleSetProps) networkRuleSetResource {
	return networkRuleSetResource{
		ID:         nsID + "/networkRuleSets/" + NetworkRuleSetName,
		Name:       NetworkRuleSetName,
		Type:       typ,
		Location:   location,
		Properties: *p,
	}
}

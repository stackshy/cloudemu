package logic

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	// workflowIPCount and connectorIPCount are how many addresses each list in
	// endpointsConfiguration carries.
	workflowIPCount  = 4
	connectorIPCount = 2
	// callbackAPIVersion is the api-version real Azure stamps on a Consumption
	// trigger callback URL.
	callbackAPIVersion = "2016-10-01"
	// callbackSASVersion is the fixed sv query value of a callback URL.
	callbackSASVersion = "1.0"
	// defaultCallbackMethod is the method a Request trigger accepts when its
	// inputs name none.
	defaultCallbackMethod = "POST"
)

// FlowEndpoints is one half (workflow or connector) of a workflow's
// endpointsConfiguration.
type FlowEndpoints struct {
	OutgoingIPAddresses       []string
	AccessEndpointIPAddresses []string
}

// EndpointsConfiguration is the service-owned properties.endpointsConfiguration
// block: the IP addresses the workflow and its managed connectors call out from
// and are reached on.
type EndpointsConfiguration struct {
	Workflow  FlowEndpoints
	Connector FlowEndpoints
}

// Endpoints returns the workflow's endpointsConfiguration. Real Azure assigns
// the addresses per region, so every workflow in a region reports the same
// set; the emulator derives them deterministically from the region so they are
// stable across gets and a restart and need no persistence.
func (wf *Workflow) Endpoints() EndpointsConfiguration {
	region := regionSegment(wf.Location)

	return EndpointsConfiguration{
		Workflow: FlowEndpoints{
			OutgoingIPAddresses:       regionIPs(region, "workflow/outgoing", workflowIPCount),
			AccessEndpointIPAddresses: regionIPs(region, "workflow/access", workflowIPCount),
		},
		Connector: FlowEndpoints{
			OutgoingIPAddresses:       regionIPs(region, "connector/outgoing", connectorIPCount),
			AccessEndpointIPAddresses: regionIPs(region, "connector/access", connectorIPCount),
		},
	}
}

// regionIPs derives n stable public-looking IPv4 addresses for region and kind.
func regionIPs(region, kind string, n int) []string {
	out := make([]string, n)

	for i := range n {
		sum := sha256.Sum256(fmt.Appendf(nil, "cloudemu/logic/%s/%s/%d", region, kind, i))
		out[i] = fmt.Sprintf("20.%d.%d.%d", sum[0], sum[1], 1+sum[2]%254)
	}

	return out
}

// regionSegment is the location lowercased with spaces removed ("West Europe"
// -> "westeurope"), falling back to defaultRegion when empty.
func regionSegment(location string) string {
	region := strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if region == "" {
		return defaultRegion
	}

	return region
}

// CallbackURLQueries are the query parameters of a trigger callback URL.
type CallbackURLQueries struct {
	APIVersion string
	Sp         string
	Sv         string
	Sig        string
}

// CallbackURL is the result of POST .../triggers/{t}/listCallbackUrl.
type CallbackURL struct {
	Value        string
	Method       string
	BasePath     string
	RelativePath string
	Queries      CallbackURLQueries
}

// ListCallbackURL returns the callback URL of trigger triggerName, in the shape
// real Azure mints for a Consumption workflow:
//
//	<accessEndpoint>/triggers/<t>/paths/invoke?api-version=2016-10-01&sp=%2Ftriggers%2F<t>%2Frun&sv=1.0&sig=<sig>
//
// The signature is derived from the workflow identity and trigger name, so it is
// stable across calls and a restart. A missing workflow, or a definition with no
// trigger of that name (matched case-insensitively), is a NotFound error.
func (m *Mock) ListCallbackURL(_ context.Context, sub, rg, name, triggerName string) (CallbackURL, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wf, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return CallbackURL{}, notFound(rg, name)
	}

	trigger, inputs, ok := findTrigger(wf.Definition, triggerName)
	if !ok {
		return CallbackURL{}, cerrors.Newf(cerrors.NotFound,
			"The workflow '%s' does not contain trigger '%s'.", name, triggerName)
	}

	basePath := wf.AccessEndpoint + "/triggers/" + trigger + "/paths/invoke"
	sp := "/triggers/" + trigger + "/run"
	sig := callbackSig(wf.ARMID(), trigger)

	method := defaultCallbackMethod
	if inputs.Method != "" {
		method = strings.ToUpper(inputs.Method)
	}

	return CallbackURL{
		// Assembled by hand rather than with url.Values.Encode, which sorts
		// keys: real Azure emits api-version, sp, sv, sig in that order.
		Value: basePath + "?api-version=" + callbackAPIVersion +
			"&sp=" + url.QueryEscape(sp) + "&sv=" + callbackSASVersion + "&sig=" + sig,
		Method:       method,
		BasePath:     basePath,
		RelativePath: inputs.RelativePath,
		Queries: CallbackURLQueries{
			APIVersion: callbackAPIVersion,
			Sp:         sp,
			Sv:         callbackSASVersion,
			Sig:        sig,
		},
	}, nil
}

// triggerInputs is the subset of a trigger's inputs the callback URL reports.
type triggerInputs struct {
	Method       string `json:"method"`
	RelativePath string `json:"relativePath"`
}

// findTrigger looks up triggerName (case-insensitively) in the definition's
// triggers map, returning the stored name and the trigger's inputs.
func findTrigger(definition json.RawMessage, triggerName string) (string, triggerInputs, bool) {
	var doc struct {
		Triggers map[string]struct {
			Inputs triggerInputs `json:"inputs"`
		} `json:"triggers"`
	}

	if len(definition) == 0 || json.Unmarshal(definition, &doc) != nil {
		return "", triggerInputs{}, false
	}

	for n, t := range doc.Triggers {
		if strings.EqualFold(n, triggerName) {
			return n, t.Inputs, true
		}
	}

	return "", triggerInputs{}, false
}

// callbackSig is the deterministic SAS signature of a trigger callback URL: a
// 43-character unpadded base64url token, the shape real signatures take.
func callbackSig(armID, trigger string) string {
	sum := sha256.Sum256([]byte("cloudemu/logic/callback/" + strings.ToLower(armID) + "/" + strings.ToLower(trigger)))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

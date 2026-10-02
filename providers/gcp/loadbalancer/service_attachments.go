package loadbalancer

import (
	"context"
	"encoding/json"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Compile-time check that Mock implements the service-attachment capability.
var _ driver.GCPServiceAttachmentStore = (*Mock)(nil)

// Service attachment body members the provider reads or owns.
const (
	saConnectionPreference = "connectionPreference"
	saNatSubnets           = "natSubnets"
	saTargetService        = "targetService"
	saAcceptLists          = "consumerAcceptLists"
	saRejectLists          = "consumerRejectLists"
	saConnectedEndpoints   = "connectedEndpoints"

	epEndpoint        = "endpoint"
	epPscConnectionID = "pscConnectionId"
	epStatus          = "status"
	epConsumerNetwork = "consumerNetwork"

	acceptProject  = "projectIdOrNum"
	acceptNetwork  = "networkUrl"
	acceptLimit    = "connectionLimit"
	acceptEndpoint = "endpointUrl"
)

// InsertGCPServiceAttachment validates and stores a new service attachment
// with no connected endpoints.
//
//nolint:gocritic // hugeParam: interface method signature is fixed.
func (m *Mock) InsertGCPServiceAttachment(ctx context.Context, res driver.GCPResource) error {
	body := cloneBody(res.Body)
	delete(body, saConnectedEndpoints)

	if err := validateServiceAttachment(body); err != nil {
		return err
	}

	res.Collection = driver.GCPServiceAttachmentCollection
	res.Body = body

	err := m.PutGCPResource(ctx, res)
	if cerrors.IsAlreadyExists(err) {
		return cerrors.Newf(cerrors.AlreadyExists, "The resource 'serviceAttachments/%s' already exists", res.Name)
	}

	return err
}

// GetGCPServiceAttachment returns the named attachment, or NotFound.
func (m *Mock) GetGCPServiceAttachment(_ context.Context, region, name string) (*driver.GCPResource, error) {
	res, ok := m.gcpResources.Get(gcpResourceKey(driver.GCPServiceAttachmentCollection, region, name))
	if !ok {
		return nil, serviceAttachmentNotFound(name)
	}

	res.Body = cloneBody(res.Body)

	return &res, nil
}

// ListGCPServiceAttachments returns every attachment in region.
func (m *Mock) ListGCPServiceAttachments(ctx context.Context, region string) ([]driver.GCPResource, error) {
	items, err := m.ListGCPResources(ctx, driver.GCPServiceAttachmentCollection, region)
	for i := range items {
		items[i].Body = cloneBody(items[i].Body)
	}

	return items, err
}

// UpdateGCPServiceAttachment applies mutate under the store lock, keeps the
// connected endpoints (they are output-only), validates the result and
// re-evaluates every connection against the new lists.
func (m *Mock) UpdateGCPServiceAttachment(_ context.Context, region, name string,
	mutate func(*driver.GCPResource) error,
) error {
	return m.updateServiceAttachment(region, name, func(res *driver.GCPResource) error {
		endpoints := cloneBody(res.Body)[saConnectedEndpoints]

		next := *res
		next.Body = cloneBody(res.Body)

		if err := mutate(&next); err != nil {
			return err
		}

		next.Body = cloneBody(next.Body)
		delete(next.Body, saConnectedEndpoints)

		if endpoints != nil {
			next.Body[saConnectedEndpoints] = endpoints
		}

		if err := validateServiceAttachment(next.Body); err != nil {
			return err
		}

		*res = next

		return nil
	})
}

// DeleteGCPServiceAttachment removes the named attachment, or NotFound.
func (m *Mock) DeleteGCPServiceAttachment(_ context.Context, region, name string) error {
	if !m.gcpResources.Delete(gcpResourceKey(driver.GCPServiceAttachmentCollection, region, name)) {
		return serviceAttachmentNotFound(name)
	}

	return nil
}

// ConnectGCPServiceAttachment appends a consumer endpoint and returns the
// status the evaluation gave it.
func (m *Mock) ConnectGCPServiceAttachment(_ context.Context, region, name string,
	ep driver.GCPPSCEndpoint,
) (string, error) {
	status := ""

	err := m.updateServiceAttachment(region, name, func(res *driver.GCPResource) error {
		body := cloneBody(res.Body)
		eps := endpointsOf(body)

		eps = append(eps, map[string]any{
			epEndpoint:        ep.Endpoint,
			epPscConnectionID: ep.PscConnectionID,
			epConsumerNetwork: ep.ConsumerNetwork,
		})

		setEndpoints(body, eps)
		res.Body = body

		return nil
	})
	if err != nil {
		return "", err
	}

	res, _ := m.gcpResources.Get(gcpResourceKey(driver.GCPServiceAttachmentCollection, region, name))
	for _, e := range endpointsOf(res.Body) {
		if e[epPscConnectionID] == ep.PscConnectionID {
			status, _ = e[epStatus].(string)
		}
	}

	return status, nil
}

// DisconnectGCPServiceAttachment removes a consumer endpoint by
// pscConnectionId; a missing attachment or endpoint is not an error.
func (m *Mock) DisconnectGCPServiceAttachment(_ context.Context, region, name, pscConnectionID string) error {
	err := m.updateServiceAttachment(region, name, func(res *driver.GCPResource) error {
		body := cloneBody(res.Body)
		eps := endpointsOf(body)
		kept := eps[:0]

		for _, e := range eps {
			if e[epPscConnectionID] != pscConnectionID {
				kept = append(kept, e)
			}
		}

		setEndpoints(body, kept)
		res.Body = body

		return nil
	})
	if cerrors.IsNotFound(err) {
		return nil
	}

	return err
}

// GCPPSCConnectionStatus returns a consumer endpoint's current status, or
// CLOSED when the attachment or the endpoint is gone.
func (m *Mock) GCPPSCConnectionStatus(_ context.Context, region, name, pscConnectionID string) string {
	res, ok := m.gcpResources.Get(gcpResourceKey(driver.GCPServiceAttachmentCollection, region, name))
	if !ok {
		return driver.PSCStatusClosed
	}

	for _, e := range endpointsOf(res.Body) {
		if e[epPscConnectionID] == pscConnectionID {
			if s, _ := e[epStatus].(string); s != "" {
				return s
			}
		}
	}

	return driver.PSCStatusClosed
}

// updateServiceAttachment runs mutate under the store lock and then
// re-evaluates every connection. A mutate error leaves the record unchanged.
func (m *Mock) updateServiceAttachment(region, name string, mutate func(*driver.GCPResource) error) error {
	var mutateErr error

	updated := m.gcpResources.Update(gcpResourceKey(driver.GCPServiceAttachmentCollection, region, name),
		func(res driver.GCPResource) driver.GCPResource {
			next := res
			if err := mutate(&next); err != nil {
				mutateErr = err
				return res
			}

			evaluateConnections(next.Body)

			return next
		})
	if !updated {
		return serviceAttachmentNotFound(name)
	}

	return mutateErr
}

// validateServiceAttachment checks the members GCP requires or constrains.
func validateServiceAttachment(body map[string]any) error {
	pref, _ := body[saConnectionPreference].(string)
	if pref != driver.PSCAcceptAutomatic && pref != driver.PSCAcceptManual {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.connectionPreference': '%v'. Must be one of [ACCEPT_AUTOMATIC, ACCEPT_MANUAL].",
			body[saConnectionPreference])
	}

	if target, _ := body[saTargetService].(string); target == "" {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'resource.targetService': ''. The target service must be specified.")
	}

	if subnets, _ := body[saNatSubnets].([]any); len(subnets) == 0 {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'resource.natSubnets': ''. At least one NAT subnetwork must be specified.")
	}

	return validateAcceptLists(body)
}

// validateAcceptLists checks each consumerAcceptLists entry names a consumer
// and carries a non-negative connectionLimit.
func validateAcceptLists(body map[string]any) error {
	accept, _ := body[saAcceptLists].([]any)
	for i, raw := range accept {
		entry, _ := raw.(map[string]any)
		project, _ := entry[acceptProject].(string)
		network, _ := entry[acceptNetwork].(string)
		endpoint, _ := entry[acceptEndpoint].(string)

		if project == "" && network == "" && endpoint == "" {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value for field 'resource.consumerAcceptLists[%d]': an entry must name a projectIdOrNum, networkUrl or endpointUrl.", i)
		}

		if limit, present := entry[acceptLimit]; present {
			if n, ok := jsonNumber(limit); !ok || n < 0 {
				return cerrors.Newf(cerrors.InvalidArgument,
					"Invalid value for field 'resource.consumerAcceptLists[%d].connectionLimit': '%v'.", i, limit)
			}
		}
	}

	return nil
}

// evaluateConnections recomputes every connected endpoint's status in
// connection order, so an earlier consumer keeps its place under a limit.
func evaluateConnections(body map[string]any) {
	eps := endpointsOf(body)
	if len(eps) == 0 {
		return
	}

	manual := body[saConnectionPreference] == driver.PSCAcceptManual
	accept, _ := body[saAcceptLists].([]any)
	used := make([]int64, len(accept))

	for _, e := range eps {
		endpoint, _ := e[epEndpoint].(string)
		network, _ := e[epConsumerNetwork].(string)

		e[epStatus] = connectionStatus(manual, body, accept, used, pscConsumer{
			project: projectOf(endpoint), network: network, endpoint: endpoint,
		})
	}

	setEndpoints(body, eps)
}

// connectionStatus decides one endpoint's status, charging an accepted one
// against the accept-list entry that admitted it.
func connectionStatus(manual bool, body map[string]any, accept []any, used []int64, c pscConsumer) string {
	if !manual {
		return driver.PSCStatusAccepted
	}

	reject, _ := body[saRejectLists].([]any)
	for _, r := range reject {
		if s, _ := r.(string); s != "" && (s == c.project || sameNetwork(s, c.network)) {
			return driver.PSCStatusRejected
		}
	}

	for i, raw := range accept {
		entry, _ := raw.(map[string]any)
		if !c.matches(entry) {
			continue
		}

		if limit, limited := jsonNumber(entry[acceptLimit]); limited && used[i] >= limit {
			return driver.PSCStatusPending
		}

		used[i]++

		return driver.PSCStatusAccepted
	}

	return driver.PSCStatusPending
}

// pscConsumer identifies a connecting endpoint for the accept/reject lists.
type pscConsumer struct {
	project, network, endpoint string
}

// matches reports whether an accept-list entry names this consumer by
// project, network or endpoint URL.
func (c pscConsumer) matches(entry map[string]any) bool {
	p, _ := entry[acceptProject].(string)
	n, _ := entry[acceptNetwork].(string)
	e, _ := entry[acceptEndpoint].(string)

	return (p != "" && p == c.project) || sameNetwork(n, c.network) || (e != "" && sameNetwork(e, c.endpoint))
}

// sameNetwork compares two network references by their
// projects/{p}/global/networks/{n} tail, so a full URL and a relative path of
// the same network match.
func sameNetwork(a, b string) bool {
	return a != "" && b != "" && networkTail(a) == networkTail(b)
}

func networkTail(ref string) string {
	if i := strings.Index(ref, "projects/"); i >= 0 {
		return ref[i:]
	}

	return ref
}

// projectOf extracts the project id from a ".../projects/{p}/..." reference.
func projectOf(ref string) string {
	const marker = "projects/"

	i := strings.Index(ref, marker)
	if i < 0 {
		return ""
	}

	rest := ref[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}

	return rest
}

// endpointsOf reads the connectedEndpoints list of a body.
func endpointsOf(body map[string]any) []map[string]any {
	raw, _ := body[saConnectedEndpoints].([]any)
	out := make([]map[string]any, 0, len(raw))

	for _, r := range raw {
		if e, ok := r.(map[string]any); ok {
			out = append(out, e)
		}
	}

	return out
}

// setEndpoints writes the connectedEndpoints list, removing it when empty.
func setEndpoints(body map[string]any, eps []map[string]any) {
	if len(eps) == 0 {
		delete(body, saConnectedEndpoints)
		return
	}

	list := make([]any, 0, len(eps))
	for _, e := range eps {
		list = append(list, e)
	}

	body[saConnectedEndpoints] = list
}

// jsonNumber reads an integral JSON value: a number, or a decimal string (the
// proto JSON encoding of an int64/uint32 field may be either).
func jsonNumber(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := json.Number(t).Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

// cloneBody deep-copies a decoded JSON body through a JSON round trip, so a
// caller never aliases the stored maps.
func cloneBody(body map[string]any) map[string]any {
	out := map[string]any{}

	if body == nil {
		return out
	}

	b, err := json.Marshal(body)
	if err != nil {
		return out
	}

	_ = json.Unmarshal(b, &out)

	return out
}

// serviceAttachmentNotFound renders compute's not-found message.
func serviceAttachmentNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "The resource 'serviceAttachments/%s' was not found", name)
}

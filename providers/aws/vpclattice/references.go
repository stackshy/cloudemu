package vpclattice

import (
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
)

// forwardAction is the part of a listener/rule action that names target groups.
type forwardAction struct {
	Forward struct {
		TargetGroups []struct {
			TargetGroupIdentifier string `json:"targetGroupIdentifier"`
		} `json:"targetGroups"`
	} `json:"forward"`
}

// actionTargetGroupIDs returns the target-group ids (the ARN form is reduced to
// its id) a raw action forwards to. A malformed action references nothing.
func actionTargetGroupIDs(action []byte) []string {
	var fa forwardAction

	if len(action) == 0 || json.Unmarshal(action, &fa) != nil {
		return nil
	}

	ids := make([]string, 0, len(fa.Forward.TargetGroups))
	for _, tg := range fa.Forward.TargetGroups {
		ids = append(ids, idFromIdentifier(tg.TargetGroupIdentifier))
	}

	return ids
}

func actionForwardsTo(action []byte, tgID string) bool {
	for _, id := range actionTargetGroupIDs(action) {
		if id == tgID {
			return true
		}
	}

	return false
}

// targetGroupServiceARNs returns the sorted, de-duplicated ARNs of every live
// service whose listener default action or rule forwards to the target group.
// Caller holds m.mu.
func (m *Mock) targetGroupServiceARNs(tgID string) []string {
	seen := map[string]struct{}{}

	for _, l := range m.listeners.All() {
		if actionForwardsTo(l.DefaultAction, tgID) {
			seen[l.ServiceARN] = struct{}{}
		}
	}

	for _, r := range m.rules.All() {
		if !actionForwardsTo(r.Action, tgID) {
			continue
		}

		if svc, ok := m.services.Get(r.ServiceID); ok {
			seen[svc.ARN] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for arn := range seen {
		out = append(out, arn)
	}

	sort.Strings(out)

	return out
}

// targetGroupInUse reports whether any listener default action or rule still
// forwards to the target group. Caller holds m.mu.
func (m *Mock) targetGroupInUse(tgID string) bool {
	for _, l := range m.listeners.All() {
		if actionForwardsTo(l.DefaultAction, tgID) {
			return true
		}
	}

	for _, r := range m.rules.All() {
		if actionForwardsTo(r.Action, tgID) {
			return true
		}
	}

	return false
}

// resourceGatewayInUse reports whether a resource configuration references the
// gateway. Caller holds m.mu.
func (m *Mock) resourceGatewayInUse(gwID string) bool {
	for _, c := range m.resourceConfigs.All() {
		if c.ResourceGatewayID == gwID {
			return true
		}
	}

	return false
}

// loggableResourceExists reports whether identifier (id or ARN) names a service
// network, service or resource configuration. Caller holds m.mu.
func (m *Mock) loggableResourceExists(identifier string) bool {
	id := idFromIdentifier(identifier)

	return m.serviceNetworks.Has(id) || m.services.Has(id) || m.resourceConfigs.Has(id)
}

// serviceNameTaken and serviceNetworkNameTaken enforce per-account unique names.
// Caller holds m.mu, which every create path holds across check and insert.
func (m *Mock) serviceNameTaken(name string) bool {
	for _, s := range m.services.All() {
		if s.Name == name {
			return true
		}
	}

	return false
}

func (m *Mock) serviceNetworkNameTaken(name string) bool {
	for _, s := range m.serviceNetworks.All() {
		if s.Name == name {
			return true
		}
	}

	return false
}

// rulePriorityTaken reports whether another rule on the listener holds the
// priority. Caller holds m.mu.
func (m *Mock) rulePriorityTaken(listenerID string, priority int32, exceptRuleID string) bool {
	for id, r := range m.rules.All() {
		if r.ListenerID == listenerID && id != exceptRuleID && r.Priority == priority {
			return true
		}
	}

	return false
}

func (m *Mock) serviceAssociationExists(snID, serviceID string) bool {
	for _, a := range m.snSvcAssocs.All() {
		if a.ServiceNetworkID == snID && a.ServiceID == serviceID {
			return true
		}
	}

	return false
}

func conflictf(format string, args ...any) error {
	return errors.Newf(errors.AlreadyExists, format, args...)
}

func inUsef(format string, args ...any) error {
	return errors.Newf(errors.FailedPrecondition, format, args...)
}

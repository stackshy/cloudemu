package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Detailed-monitoring wire states (Instance.Monitoring.State enum). MonitorInstances
// transitions to "pending" and UnmonitorInstances to "disabling"; a settled
// instance reports "enabled" or "disabled".
const (
	monitorStateDisabled  = "disabled"
	monitorStateDisabling = "disabling"
	monitorStateEnabled   = "enabled"
	monitorStatePending   = "pending"
)

func (h *Handler) routeInstanceStatus(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "MonitorInstances":
		h.monitorInstances(w, r, true)
	case "UnmonitorInstances":
		h.monitorInstances(w, r, false)
	case "DescribeInstanceStatus":
		h.describeInstanceStatus(w, r)
	default:
		return false
	}

	return true
}

type monitorItemXML struct {
	InstanceID string `xml:"instanceId"`
	State      string `xml:"monitoring>state"`
}

type monitorInstancesResponseXML struct {
	XMLName   xml.Name         `xml:"MonitorInstancesResponse"`
	Xmlns     string           `xml:"xmlns,attr"`
	RequestID string           `xml:"requestId"`
	Instances []monitorItemXML `xml:"instancesSet>item"`
}

// monitorInstances answers Monitor/UnmonitorInstances. It validates each
// requested instance exists (InvalidInstanceID.NotFound otherwise), persists the
// new monitoring state, and echoes the transitional wire state: MonitorInstances
// returns "pending" and UnmonitorInstances "disabling" (not the settled value).
func (h *Handler) monitorInstances(w http.ResponseWriter, r *http.Request, enable bool) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	if _, err := h.compute.DescribeInstances(r.Context(), ids, nil); err != nil {
		writeErr(w, err)
		return
	}

	stored, echo := monitorStateDisabled, monitorStateDisabling
	if enable {
		stored, echo = monitorStateEnabled, monitorStatePending
	}

	if attributer, ok := h.compute.(instanceAttributer); ok {
		for _, id := range ids {
			if err := attributer.SetInstanceAttribute(r.Context(), id, attrMonitoring, stored); err != nil {
				writeErr(w, err)
				return
			}
		}
	}

	items := make([]monitorItemXML, 0, len(ids))
	for _, id := range ids {
		items = append(items, monitorItemXML{InstanceID: id, State: echo})
	}

	awsquery.WriteXMLResponse(w, monitorInstancesResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, Instances: items,
	})
}

// statusCheckDetailXML is one <details><item> entry (e.g. the reachability
// check) inside a system/instance status summary.
type statusCheckDetailXML struct {
	Name   string `xml:"name"`
	Status string `xml:"status"`
}

type statusDetailXML struct {
	Status  string                 `xml:"status"`
	Details []statusCheckDetailXML `xml:"details>item,omitempty"`
}

type instanceStatusItemXML struct {
	InstanceID     string          `xml:"instanceId"`
	AvailZone      string          `xml:"availabilityZone,omitempty"`
	InstanceState  instanceState   `xml:"instanceState"`
	SystemStatus   statusDetailXML `xml:"systemStatus"`
	InstanceStatus statusDetailXML `xml:"instanceStatus"`
}

type describeInstanceStatusResponseXML struct {
	XMLName   xml.Name                `xml:"DescribeInstanceStatusResponse"`
	Xmlns     string                  `xml:"xmlns,attr"`
	RequestID string                  `xml:"requestId"`
	Statuses  []instanceStatusItemXML `xml:"instanceStatusSet>item"`
}

// describeInstanceStatus answers DescribeInstanceStatus. By default only
// running instances are reported (matching real EC2); IncludeAllInstances=true
// reports every state. Running instances report passing system/instance checks.
func (h *Handler) describeInstanceStatus(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")
	includeAll := r.Form.Get("IncludeAllInstances") == formTrue

	instances, err := h.compute.DescribeInstances(r.Context(), ids, nil)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]instanceStatusItemXML, 0, len(instances))

	for i := range instances {
		inst := &instances[i]
		if !includeAll && inst.State != stateRunning {
			continue
		}

		out = append(out, statusItem(inst))
	}

	awsquery.WriteXMLResponse(w, describeInstanceStatusResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, Statuses: out,
	})
}

func statusItem(inst *computedriver.Instance) instanceStatusItemXML {
	// Checks are "ok"/"passed" only once the instance is running; otherwise
	// "not-applicable", matching real EC2's status-check semantics.
	notApplicable := "not-applicable"
	summary, reachability := notApplicable, notApplicable

	if inst.State == stateRunning {
		summary, reachability = "ok", "passed"
	}

	detail := statusDetailXML{
		Status:  summary,
		Details: []statusCheckDetailXML{{Name: "reachability", Status: reachability}},
	}

	az := ""
	if len(inst.Zones) > 0 {
		az = inst.Zones[0]
	}

	return instanceStatusItemXML{
		InstanceID:     inst.ID,
		AvailZone:      az,
		InstanceState:  instanceState{Code: stateCode(inst.State), Name: inst.State},
		SystemStatus:   detail,
		InstanceStatus: detail,
	}
}

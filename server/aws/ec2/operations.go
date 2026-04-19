package ec2

import (
	"net/http"
	"strconv"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// reservationPrefix is our own reservation ID prefix. Real AWS uses "r-".
const reservationPrefix = "r-"

// runInstances handles Action=RunInstances.
func (h *Handler) runInstances(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	count := instanceCount(form.Get("MinCount"), form.Get("MaxCount"))

	cfg := computedriver.InstanceConfig{
		ImageID:        form.Get("ImageId"),
		InstanceType:   form.Get("InstanceType"),
		SubnetID:       form.Get("SubnetId"),
		SecurityGroups: awsquery.ListStrings(form, "SecurityGroupId"),
		KeyName:        form.Get("KeyName"),
		UserData:       form.Get("UserData"),
		Tags:           mergeTagSpecs(awsquery.TagSpecs(form), "instance"),
	}

	instances, err := h.compute.RunInstances(r.Context(), cfg, count)
	if err != nil {
		writeErr(w, err)
		return
	}

	if len(instances) == 0 {
		writeErr(w, cerrors.New(cerrors.FailedPrecondition,
			"driver returned zero instances"))

		return
	}

	awsquery.WriteXMLResponse(w, runInstancesResponse{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		ReservationID: reservationPrefix + stripInstancePrefix(instances[0].ID),
		OwnerID:       ownerID,
		Instances:     toInstanceXMLs(instances),
	})
}

// describeInstances handles Action=DescribeInstances.
func (h *Handler) describeInstances(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	ids := awsquery.ListStrings(form, "InstanceId")
	filters := toDriverFilters(awsquery.Filters(form))

	instances, err := h.compute.DescribeInstances(r.Context(), ids, filters)
	if err != nil {
		writeErr(w, err)
		return
	}

	// We don't track real reservation groupings — each instance gets its own
	// singleton reservation. SDK clients are happy with this shape.
	reservations := make([]reservationXML, 0, len(instances))
	for i := range instances {
		reservations = append(reservations, reservationXML{
			ReservationID: reservationPrefix + stripInstancePrefix(instances[i].ID),
			OwnerID:       ownerID,
			Instances:     toInstanceXMLs(instances[i : i+1]),
		})
	}

	awsquery.WriteXMLResponse(w, describeInstancesResponse{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		Reservations: reservations,
	})
}

// startInstances handles Action=StartInstances.
func (h *Handler) startInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	if err := h.compute.StartInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, startInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChanges(ids,
			instanceState{Code: stateCodePending, Name: "pending"},
			instanceState{Code: stateCodeStopped, Name: "stopped"}),
	})
}

// stopInstances handles Action=StopInstances.
func (h *Handler) stopInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	if err := h.compute.StopInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, stopInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChanges(ids,
			instanceState{Code: stateCodeStopping, Name: "stopping"},
			instanceState{Code: stateCodeRunning, Name: "running"}),
	})
}

// rebootInstances handles Action=RebootInstances.
func (h *Handler) rebootInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	if err := h.compute.RebootInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, rebootInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// terminateInstances handles Action=TerminateInstances.
func (h *Handler) terminateInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	if err := h.compute.TerminateInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, terminateInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChanges(ids,
			instanceState{Code: stateCodeShuttingDown, Name: "shutting-down"},
			instanceState{Code: stateCodeRunning, Name: "running"}),
	})
}

// modifyInstanceAttribute handles Action=ModifyInstanceAttribute.
// Phase 1 scope: InstanceType only. Other attributes land in later phases.
func (h *Handler) modifyInstanceAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")

	input := computedriver.ModifyInstanceInput{
		InstanceType: r.Form.Get("InstanceType.Value"),
	}

	// If nothing to modify, still return success (real EC2 behavior).
	if input.InstanceType == "" {
		awsquery.WriteXMLResponse(w, modifyInstanceAttributeResponse{
			Xmlns:     awsquery.Namespace,
			RequestID: awsquery.RequestID,
			Return:    true,
		})

		return
	}

	if err := h.compute.ModifyInstance(r.Context(), id, input); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyInstanceAttributeResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// instanceCount returns how many instances RunInstances should launch.
// Real EC2 launches MaxCount instances when capacity allows, falling back to
// fewer (but at least MinCount) when it doesn't. Our in-memory backend has
// unlimited capacity, so we always launch MaxCount. Unparsable / missing
// MaxCount defaults to MinCount; both missing defaults to 1.
func instanceCount(minStr, maxStr string) int {
	minN, _ := strconv.Atoi(minStr)
	maxN, _ := strconv.Atoi(maxStr)

	if maxN < 1 {
		maxN = minN
	}

	if maxN < 1 {
		maxN = 1
	}

	return maxN
}

// mergeTagSpecs flattens tag-specifications whose ResourceType matches
// resource ("instance", "volume", etc.) into a single map.
func mergeTagSpecs(specs []awsquery.TagSpec, resource string) map[string]string {
	if len(specs) == 0 {
		return nil
	}

	out := make(map[string]string)

	for _, s := range specs {
		if s.ResourceType != "" && s.ResourceType != resource {
			continue
		}

		for k, v := range s.Tags {
			out[k] = v
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// toInstanceXMLs converts driver instances to their XML wire form.
func toInstanceXMLs(instances []computedriver.Instance) []instanceXML {
	out := make([]instanceXML, 0, len(instances))

	for i := range instances {
		inst := &instances[i]
		xi := instanceXML{
			InstanceID:   inst.ID,
			ImageID:      inst.ImageID,
			InstanceType: inst.InstanceType,
			State:        instanceState{Code: stateCode(inst.State), Name: inst.State},
			LaunchTime:   inst.LaunchTime,
			SubnetID:     inst.SubnetID,
			VPCID:        inst.VPCID,
			PrivateIP:    inst.PrivateIP,
			PublicIP:     inst.PublicIP,
		}

		for _, sg := range inst.SecurityGroups {
			xi.Groups = append(xi.Groups, groupItem{GroupID: sg})
		}

		for k, v := range inst.Tags {
			xi.Tags = append(xi.Tags, tagItem{Key: k, Value: v})
		}

		out = append(out, xi)
	}

	return out
}

// toDriverFilters converts parsed filters to the driver's filter shape.
func toDriverFilters(in []awsquery.Filter) []computedriver.DescribeFilter {
	if len(in) == 0 {
		return nil
	}

	out := make([]computedriver.DescribeFilter, 0, len(in))
	for _, f := range in {
		out = append(out, computedriver.DescribeFilter{Name: f.Name, Values: f.Values})
	}

	return out
}

// stateChanges builds a transition record for each id, reporting the same
// current/previous states. Real AWS returns these exact codes for each op.
func stateChanges(ids []string, current, previous instanceState) []stateChangeXML {
	out := make([]stateChangeXML, 0, len(ids))
	for _, id := range ids {
		out = append(out, stateChangeXML{
			InstanceID:    id,
			CurrentState:  current,
			PreviousState: previous,
		})
	}

	return out
}

// stripInstancePrefix removes the "i-" prefix so we can reuse the body as a
// reservation suffix ("r-<body>").
func stripInstancePrefix(id string) string {
	const prefix = "i-"
	if len(id) > len(prefix) && id[:len(prefix)] == prefix {
		return id[len(prefix):]
	}

	return id
}

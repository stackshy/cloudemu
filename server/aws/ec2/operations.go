package ec2

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// instanceAttributer is an AWS-specific optional capability for reading and
// writing instance attributes (DisableApiTermination, SourceDestCheck, …) that
// are not part of the portable Compute driver. The handler type-asserts for it
// so providers that don't support these attributes are unaffected.
type instanceAttributer interface {
	SetInstanceAttribute(ctx context.Context, id, name, value string) error
	GetInstanceAttribute(ctx context.Context, id, name string) (string, error)
}

// Attribute element names shared by ModifyInstanceAttribute (Name.Value on the
// wire) and DescribeInstanceAttribute (Attribute= selector).
const (
	attrDisableAPITermination = "disableApiTermination"
	attrSourceDestCheck       = "sourceDestCheck"
	attrInstanceType          = "instanceType"
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
		UserData:       decodeUserData(form.Get("UserData")),
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

	includeManaged, _ := strconv.ParseBool(form.Get("IncludeManagedResources"))

	instances, err := h.compute.DescribeInstances(r.Context(), ids, filters,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: includeManaged})
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

	prev := h.priorStates(r.Context(), ids)

	if err := h.compute.StartInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, startInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChangesFrom(ids, prev,
			instanceState{Code: stateCodePending, Name: "pending"}),
	})
}

// stopInstances handles Action=StopInstances.
func (h *Handler) stopInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	prev := h.priorStates(r.Context(), ids)

	if err := h.compute.StopInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, stopInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChangesFrom(ids, prev,
			instanceState{Code: stateCodeStopping, Name: "stopping"}),
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

	prev := h.priorStates(r.Context(), ids)

	if err := h.compute.TerminateInstances(r.Context(), ids); err != nil {
		// Termination protection surfaces as OperationNotPermitted, not the
		// generic instance-state error.
		if cerrors.IsPermissionDenied(err) {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "OperationNotPermitted", err.Error())
			return
		}

		writeErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, terminateInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChangesFrom(ids, prev,
			instanceState{Code: stateCodeShuttingDown, Name: "shutting-down"}),
	})
}

// modifyInstanceAttribute handles Action=ModifyInstanceAttribute. InstanceType
// changes route through the portable driver (which enforces the stopped
// precondition); DisableApiTermination / SourceDestCheck route through the
// AWS-specific instanceAttributer so they take effect (previously accepted and
// silently discarded — a false success dangerous for IaC).
func (h *Handler) modifyInstanceAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")

	if instanceType := r.Form.Get("InstanceType.Value"); instanceType != "" {
		if err := h.compute.ModifyInstance(r.Context(),
			id, computedriver.ModifyInstanceInput{InstanceType: instanceType}); err != nil {
			writeErr(w, err)
			return
		}
	}

	if err := h.applyInstanceAttributes(r, id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyInstanceAttributeResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// applyInstanceAttributes writes the boolean instance attributes present in the
// request through the instanceAttributer capability.
func (h *Handler) applyInstanceAttributes(r *http.Request, id string) error {
	attributer, ok := h.compute.(instanceAttributer)
	if !ok {
		return nil
	}

	for _, name := range []string{attrDisableAPITermination, attrSourceDestCheck} {
		if v := r.Form.Get(attrForm(name) + ".Value"); v != "" {
			if err := attributer.SetInstanceAttribute(r.Context(), id, name, v); err != nil {
				return err
			}
		}
	}

	return nil
}

// describeInstanceAttribute handles Action=DescribeInstanceAttribute, returning
// the single attribute named by Attribute= so ModifyInstanceAttribute changes
// are verifiable.
func (h *Handler) describeInstanceAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")
	attr := r.Form.Get("Attribute")

	attributer, ok := h.compute.(instanceAttributer)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "instance attributes are not supported"))
		return
	}

	val, err := attributer.GetInstanceAttribute(r.Context(), id, attr)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := describeInstanceAttributeResponse{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		InstanceID: id,
	}

	switch attr {
	case attrDisableAPITermination:
		resp.DisableAPITermination = &attributeBooleanValueXML{Value: val == formTrue}
	case attrSourceDestCheck:
		resp.SourceDestCheck = &attributeBooleanValueXML{Value: val == formTrue}
	case attrInstanceType:
		resp.InstanceType = &attributeValueXML{Value: val}
	}

	awsquery.WriteXMLResponse(w, resp)
}

// attrForm maps an attribute element name (lowerCamel, as on the response) to
// the request parameter's PascalCase prefix used by ModifyInstanceAttribute.
func attrForm(name string) string {
	switch name {
	case attrDisableAPITermination:
		return "DisableApiTermination"
	case attrSourceDestCheck:
		return "SourceDestCheck"
	default:
		return name
	}
}

// getConsoleOutput handles Action=GetConsoleOutput. The console output is read
// from the real ComputeEngine backing the instance (empty for an instance with
// no engine wired). Real EC2 returns the output base64-encoded in <output>.
func (h *Handler) getConsoleOutput(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")

	reader, ok := h.compute.(computedriver.ConsoleReader)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "console output is not supported"))
		return
	}

	out, err := reader.GetConsoleOutput(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getConsoleOutputResponse{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		InstanceID: id,
		Timestamp:  formatTime(time.Now().UTC()),
		Output:     base64.StdEncoding.EncodeToString(out),
	})
}

// decodeUserData decodes the base64 UserData the wire carries. Real EC2 UserData
// is always base64-encoded; a value that does not decode is passed through raw
// so a client that sent plain text still gets its boot script.
func decodeUserData(s string) string {
	if s == "" {
		return ""
	}

	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(decoded)
	}

	return s
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

		if inst.Operator != nil {
			xi.Operator = &operatorXML{
				Managed:         inst.Operator.Managed,
				Principal:       inst.Operator.Principal,
				HiddenByDefault: inst.Operator.HiddenByDefault,
			}
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

// priorStates captures each instance's state before a lifecycle operation so
// the response can report the real previousState (rather than a hardcoded one).
// A describe error yields an empty map; callers fall back to a zero previous.
func (h *Handler) priorStates(ctx context.Context, ids []string) map[string]instanceState {
	out := make(map[string]instanceState, len(ids))

	instances, err := h.compute.DescribeInstances(ctx, ids, nil)
	if err != nil {
		return out
	}

	for i := range instances {
		out[instances[i].ID] = instanceState{
			Code: stateCode(instances[i].State), Name: instances[i].State,
		}
	}

	return out
}

// stateChangesFrom builds a transition record for each id, deriving
// previousState from the states captured before the operation ran.
func stateChangesFrom(ids []string, previous map[string]instanceState, current instanceState) []stateChangeXML {
	out := make([]stateChangeXML, 0, len(ids))
	for _, id := range ids {
		out = append(out, stateChangeXML{
			InstanceID:    id,
			CurrentState:  current,
			PreviousState: previous[id],
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

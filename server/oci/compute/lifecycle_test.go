package compute_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// otherCompartment is a compartment other than the mock's default, so a launch
// into it proves the placement rather than matching by accident.
const otherCompartment = "ocid1.compartment.oc1..other"

// launchInto launches an instance into the named compartment and returns its
// OCID.
func (h *harness) launchInto(compartmentID string) string {
	h.t.Helper()

	var inst struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", map[string]any{
		"compartmentId":      compartmentID,
		"availabilityDomain": "cloudemu:US-ASHBURN-1-AD-1",
		"shape":              shape,
		"displayName":        "web-1",
		"imageId":            h.image,
		"createVnicDetails":  map[string]any{"subnetId": h.subnet},
	}), &inst)

	require.NotEmpty(h.t, inst.ID)

	return inst.ID
}

// decodeBody reads a JSON error body from a response the harness will not take
// (it requires a success status).
func decodeBody(resp *http.Response, out any) error {
	return json.NewDecoder(resp.Body).Decode(out)
}

// TestLaunchPlacesItsResourcesInTheCallersCompartment pins that everything a
// launch creates lands where the caller asked. OCI lists a VNIC attachment and
// a boot volume by compartment, so leaving them in the mock's default one hides
// the instance's own network interface from the account that launched it.
func TestLaunchPlacesItsResourcesInTheCallersCompartment(t *testing.T) {
	h := newHarness(t)
	id := h.launchInto(otherCompartment)

	var vnicAttachments []struct {
		ID            string `json:"id"`
		CompartmentID string `json:"compartmentId"`
		InstanceID    string `json:"instanceId"`
		SubnetID      string `json:"subnetId"`
	}

	h.decodeList(h.do(http.MethodGet,
		"/20160918/vnicAttachments?compartmentId="+otherCompartment+"&instanceId="+id, nil), &vnicAttachments)
	require.Len(t, vnicAttachments, 1, "the launch's VNIC attachment is listed in the caller's compartment")
	assert.Equal(t, otherCompartment, vnicAttachments[0].CompartmentID)
	assert.Equal(t, h.subnet, vnicAttachments[0].SubnetID)

	var bootAttachments []struct {
		CompartmentID string `json:"compartmentId"`
		BootVolumeID  string `json:"bootVolumeId"`
	}

	h.decodeList(h.do(http.MethodGet,
		"/20160918/bootVolumeAttachments?compartmentId="+otherCompartment+
			"&availabilityDomain=cloudemu:US-ASHBURN-1-AD-1&instanceId="+id, nil), &bootAttachments)
	require.Len(t, bootAttachments, 1)
	assert.Equal(t, otherCompartment, bootAttachments[0].CompartmentID)

	var bootVolumes []struct {
		ID            string `json:"id"`
		CompartmentID string `json:"compartmentId"`
	}

	h.decodeList(h.do(http.MethodGet,
		"/20160918/bootVolumes?compartmentId="+otherCompartment+
			"&availabilityDomain=cloudemu:US-ASHBURN-1-AD-1", nil), &bootVolumes)
	require.Len(t, bootVolumes, 1)
	assert.Equal(t, bootAttachments[0].BootVolumeID, bootVolumes[0].ID)

	// And none of it leaks into the mock's default compartment.
	h.decodeList(h.do(http.MethodGet,
		"/20160918/vnicAttachments?compartmentId="+compartment, nil), &vnicAttachments)
	assert.Empty(t, vnicAttachments)
}

// TestInstanceActionStopOnStoppedIsAConflict pins OCI's InstanceAction
// semantics: STOP on a STOPPED instance is 409 IncorrectState, while START on a
// RUNNING one stays the documented no-op.
func TestInstanceActionStopOnStoppedIsAConflict(t *testing.T) {
	h := newHarness(t)
	id := h.launchInto(compartment)

	resp := h.do(http.MethodPost, "/20160918/instances/"+id+"?action=STOP", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.do(http.MethodPost, "/20160918/instances/"+id+"?action=STOP", nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	require.NoError(t, decodeBody(resp, &body))
	assert.Equal(t, "IncorrectState", body.Code)
	assert.Contains(t, body.Message, "stopped")

	// The internal error-code prefix is CloudEmu's, not OCI's, and must not
	// reach the wire.
	assert.NotContains(t, body.Message, "FailedPrecondition:")

	// START is idempotent on a RUNNING instance.
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/20160918/instances/"+id+"?action=START", nil).StatusCode)
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/20160918/instances/"+id+"?action=START", nil).StatusCode)
}

// TestAttachVolumeTwiceIsAConflictWithACleanMessage pins the error body of a
// double attach: OCI's code, and a message with no CloudEmu error-code prefix.
func TestAttachVolumeTwiceIsAConflictWithACleanMessage(t *testing.T) {
	h := newHarness(t)
	id := h.launchInto(compartment)

	var vol struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumes", map[string]any{
		"compartmentId":      compartment,
		"availabilityDomain": "cloudemu:US-ASHBURN-1-AD-1",
		"sizeInGBs":          50,
	}), &vol)

	attach := map[string]any{"instanceId": id, "volumeId": vol.ID, "type": "paravirtualized"}
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/20160918/volumeAttachments", attach).StatusCode)

	resp := h.do(http.MethodPost, "/20160918/volumeAttachments", attach)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	require.NoError(t, decodeBody(resp, &body))
	assert.Equal(t, "Conflict", body.Code)
	assert.Contains(t, body.Message, "already attached")
	assert.NotContains(t, body.Message, "AlreadyExists:")
}

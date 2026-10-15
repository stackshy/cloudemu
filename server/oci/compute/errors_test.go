package compute_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doRaw sends body as is, so a test can send what json.Encoder would not.
func (h *harness) doRaw(method, path, body string) *http.Response {
	h.t.Helper()

	req, err := http.NewRequestWithContext(h.t.Context(), method, h.server.URL+path, strings.NewReader(body))
	require.NoError(h.t, err)

	resp, err := h.server.Client().Do(req)
	require.NoError(h.t, err)

	h.t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

func TestMalformedBodiesAreRefused(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/20160918/instances"},
		{http.MethodPut, "/20160918/instances/" + h.launch("web-1")},
		{http.MethodPost, "/20160918/instances/ocid1.instance.oc1.iad.x/actions/changeCompartment"},
		{http.MethodPost, "/20160918/images"},
		{http.MethodPut, "/20160918/images/ocid1.image.oc1.iad.x"},
		{http.MethodPost, "/20160918/volumes"},
		{http.MethodPut, "/20160918/volumes/ocid1.volume.oc1.iad.x"},
		{http.MethodPost, "/20160918/volumeAttachments"},
		{http.MethodPost, "/20160918/bootVolumes"},
		{http.MethodPut, "/20160918/bootVolumes/ocid1.bootvolume.oc1.iad.x"},
		{http.MethodPost, "/20160918/bootVolumeAttachments"},
		{http.MethodPost, "/20160918/volumeBackups"},
		{http.MethodPut, "/20160918/volumeBackups/ocid1.volumebackup.oc1.iad.x"},
		{http.MethodPost, "/20160918/volumeGroups"},
		{http.MethodPut, "/20160918/volumeGroups/ocid1.volumegroup.oc1.iad.x"},
		{http.MethodPost, "/20160918/vnicAttachments"},
		{http.MethodPost, "/20160918/instanceConfigurations"},
		{http.MethodPut, "/20160918/instanceConfigurations/ocid1.instanceconfiguration.oc1.iad.x"},
		{http.MethodPost, "/20160918/instancePools"},
		{http.MethodPut, "/20160918/instancePools/ocid1.instancepool.oc1.iad.x"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := h.doRaw(tc.method, tc.path, "{not json")
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Contains(t, bodyOf(t, resp), "invalid JSON")
		})
	}
}

func TestUnknownResourcesAreNotFound(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/20160918/images/ocid1.image.oc1.iad.missing"},
		{http.MethodPut, "/20160918/images/ocid1.image.oc1.iad.missing"},
		{http.MethodDelete, "/20160918/images/ocid1.image.oc1.iad.missing"},
		{http.MethodPut, "/20160918/instances/ocid1.instance.oc1.iad.missing"},
		{http.MethodDelete, "/20160918/instances/ocid1.instance.oc1.iad.missing"},
		{http.MethodPost, "/20160918/instances/ocid1.instance.oc1.iad.missing?action=START"},
		{http.MethodGet, "/20160918/bootVolumes/ocid1.bootvolume.oc1.iad.missing"},
		{http.MethodPut, "/20160918/bootVolumes/ocid1.bootvolume.oc1.iad.missing"},
		{http.MethodDelete, "/20160918/bootVolumes/ocid1.bootvolume.oc1.iad.missing"},
		{http.MethodGet, "/20160918/bootVolumeAttachments/ocid1.bootvolumeattachment.oc1.iad.missing"},
		{http.MethodDelete, "/20160918/bootVolumeAttachments/ocid1.bootvolumeattachment.oc1.iad.missing"},
		{http.MethodGet, "/20160918/volumeGroups/ocid1.volumegroup.oc1.iad.missing"},
		{http.MethodPut, "/20160918/volumeGroups/ocid1.volumegroup.oc1.iad.missing"},
		{http.MethodGet, "/20160918/instanceConfigurations/ocid1.instanceconfiguration.oc1.iad.missing"},
		{http.MethodGet, "/20160918/volumes/ocid1.volume.oc1.iad.missing/attachments"},
		{http.MethodGet, "/20160918/instances/ocid1.instance.oc1.iad.missing/vnics"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := h.do(tc.method, tc.path, map[string]any{})
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.Contains(t, bodyOf(t, resp), "NotAuthorizedOrNotFound")
		})
	}
}

func TestUnsupportedMethodsAreRefused(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/20160918/instances"},
		{http.MethodPatch, "/20160918/instances/ocid1.instance.oc1.iad.x"},
		{http.MethodPost, "/20160918/volumes/ocid1.volume.oc1.iad.x"},
		{http.MethodPut, "/20160918/volumeAttachments/ocid1.volumeattachment.oc1.iad.x"},
		{http.MethodPut, "/20160918/vnicAttachments/ocid1.vnicattachment.oc1.iad.x"},
		{http.MethodPost, "/20160918/shapes"},
		{http.MethodGet, "/20160918/instances/ocid1.instance.oc1.iad.x/actions/changeCompartment"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := h.do(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		})
	}
}

func TestMalformedPathIsABadRequest(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/20160918", "/20160918/instances/a/b/c/d", "/20200101/instances"} {
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}

func TestChangeCompartmentValidatesItsBody(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	resp := h.do(http.MethodPost, "/20160918/instances/"+id+"/actions/changeCompartment", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, bodyOf(t, resp), "compartmentId is required")

	resp = h.do(http.MethodPost, "/20160918/instances/ocid1.instance.oc1.iad.missing/actions/changeCompartment",
		map[string]any{"compartmentId": otherCompartment})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestInstanceActionsAndUpdates(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	noAction := h.do(http.MethodPost, "/20160918/instances/"+id, nil)
	assert.Equal(t, http.StatusBadRequest, noAction.StatusCode)

	var reshaped struct {
		Shape          string `json:"shape"`
		LifecycleState string `json:"lifecycleState"`
	}

	// OCI reshapes a running instance by rebooting it into the new shape.
	h.decode(h.do(http.MethodPut, "/20160918/instances/"+id, map[string]any{"shape": "VM.Standard2.1"}), &reshaped)
	assert.Equal(t, "VM.Standard2.1", reshaped.Shape)
	assert.Equal(t, "RUNNING", reshaped.LifecycleState)

	// Ordered: a second stop in a row is 409.
	for _, step := range []struct{ action, want string }{
		{"SOFTSTOP", "STOPPED"}, {"START", "RUNNING"}, {"RESET", "RUNNING"},
		{"SOFTRESET", "RUNNING"}, {"softstop", "STOPPED"},
	} {
		var got struct {
			LifecycleState string `json:"lifecycleState"`
		}

		h.decode(h.do(http.MethodPost, "/20160918/instances/"+id+"?action="+step.action, nil), &got)
		assert.Equal(t, step.want, got.LifecycleState, step.action)
	}

	var updated struct {
		Shape            string            `json:"shape"`
		FaultDomain      string            `json:"faultDomain"`
		Metadata         map[string]string `json:"metadata"`
		ExtendedMetadata map[string]any    `json:"extendedMetadata"`
		ShapeConfig      struct {
			Ocpus float32 `json:"ocpus"`
		} `json:"shapeConfig"`
		AgentConfig struct {
			IsMonitoringDisabled bool `json:"isMonitoringDisabled"`
		} `json:"agentConfig"`
	}

	h.decode(h.do(http.MethodPut, "/20160918/instances/"+id, map[string]any{
		"shape":            "VM.Standard.E5.Flex",
		"faultDomain":      "FAULT-DOMAIN-2",
		"metadata":         map[string]string{"user_data": "aGk="},
		"extendedMetadata": map[string]any{"k": "v"},
		"shapeConfig":      map[string]any{"ocpus": 4},
		"agentConfig":      map[string]any{"isMonitoringDisabled": true},
	}), &updated)
	assert.Equal(t, "VM.Standard.E5.Flex", updated.Shape)
	assert.Equal(t, "FAULT-DOMAIN-2", updated.FaultDomain)
	assert.Equal(t, "aGk=", updated.Metadata["user_data"])
	assert.Equal(t, "v", updated.ExtendedMetadata["k"])
	assert.InDelta(t, 4.0, updated.ShapeConfig.Ocpus, 0.001)
	assert.True(t, updated.AgentConfig.IsMonitoringDisabled)

	unknownShape := h.do(http.MethodPut, "/20160918/instances/"+id, map[string]any{"shape": "VM.Nope.1"})
	assert.Equal(t, http.StatusNotFound, unknownShape.StatusCode)
}

func TestListInstancesFiltersByStateAndDomain(t *testing.T) {
	h := newHarness(t)
	running := h.launch("running")
	stopped := h.launch("stopped")

	h.decode(h.do(http.MethodPost, "/20160918/instances/"+stopped+"?action=STOP", nil), &struct{}{})

	body := h.launchBody("elsewhere")
	body["availabilityDomain"] = "cloudemu:US-ASHBURN-1-AD-2"
	h.decode(h.do(http.MethodPost, "/20160918/instances", body), &struct{}{})

	ids := func(query string) []string {
		t.Helper()

		var items []idOnly

		h.decode(h.do(http.MethodGet, "/20160918/instances?compartmentId="+compartment+query, nil), &items)

		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.ID)
		}

		return out
	}

	assert.Equal(t, []string{stopped}, ids("&lifecycleState=STOPPED"))
	assert.ElementsMatch(t, []string{running, stopped}, ids("&availabilityDomain="+testAD))
	assert.Equal(t, []string{running}, ids("&availabilityDomain="+testAD+"&lifecycleState=RUNNING"))
	assert.Empty(t, ids("&lifecycleState=TERMINATED"))
	assert.Empty(t, ids("&lifecycleState=MOVING"), "an unknown state matches nothing")

	resp := h.do(http.MethodGet, "/20160918/instances?compartmentId="+compartment+"&sortOrder=SIDEWAYS", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListsPage(t *testing.T) {
	h := newHarness(t)

	for range 3 {
		h.createVolume(50)
	}

	first := h.do(http.MethodGet, "/20160918/volumes?compartmentId="+compartment+"&limit=2", nil)
	next := first.Header.Get("opc-next-page")
	require.NotEmpty(t, next)

	var page1, page2, past []idOnly

	h.decode(first, &page1)
	assert.Len(t, page1, 2)

	h.decode(h.do(http.MethodGet, "/20160918/volumes?compartmentId="+compartment+"&limit=2&page="+next, nil), &page2)
	assert.Len(t, page2, 1)

	h.decode(h.do(http.MethodGet, "/20160918/volumes?compartmentId="+compartment+"&page=99", nil), &past)
	assert.Empty(t, past)
}

func TestLaunchCarriesItsOCIOnlyDetails(t *testing.T) {
	h := newHarness(t)

	body := h.launchBody("spot")
	delete(body, "imageId")
	body["sourceDetails"] = map[string]any{"sourceType": "image", "imageId": h.image, "bootVolumeSizeInGBs": 100}
	body["faultDomain"] = "FAULT-DOMAIN-1"
	body["agentConfig"] = map[string]any{"areAllPluginsDisabled": true}
	body["preemptibleInstanceConfig"] = map[string]any{
		"preemptionAction": map[string]any{"type": "TERMINATE", "preserveBootVolume": true},
	}

	var inst struct {
		ID            string `json:"id"`
		FaultDomain   string `json:"faultDomain"`
		SourceDetails struct {
			SourceType          string `json:"sourceType"`
			ImageID             string `json:"imageId"`
			BootVolumeSizeInGBs int    `json:"bootVolumeSizeInGBs"`
		} `json:"sourceDetails"`
		AgentConfig struct {
			AreAllPluginsDisabled bool `json:"areAllPluginsDisabled"`
		} `json:"agentConfig"`
		PreemptibleInstanceConfig *struct {
			PreemptionAction struct {
				PreserveBootVolume bool `json:"preserveBootVolume"`
			} `json:"preemptionAction"`
		} `json:"preemptibleInstanceConfig"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", body), &inst)
	assert.Equal(t, "FAULT-DOMAIN-1", inst.FaultDomain)
	assert.Equal(t, "image", inst.SourceDetails.SourceType)
	assert.Equal(t, h.image, inst.SourceDetails.ImageID)
	assert.Equal(t, 100, inst.SourceDetails.BootVolumeSizeInGBs)
	assert.True(t, inst.AgentConfig.AreAllPluginsDisabled)
	require.NotNil(t, inst.PreemptibleInstanceConfig)
	assert.True(t, inst.PreemptibleInstanceConfig.PreemptionAction.PreserveBootVolume)

	bootVolume := h.bootVolumeOf(inst.ID)

	term := h.do(http.MethodDelete, "/20160918/instances/"+inst.ID+"?preserveBootVolume=true", nil)
	require.Equal(t, http.StatusNoContent, term.StatusCode)

	relaunch := h.launchBody("reborn")
	delete(relaunch, "imageId")
	relaunch["sourceDetails"] = map[string]any{"sourceType": "bootVolume", "bootVolumeId": bootVolume}

	var reborn struct {
		SourceDetails struct {
			SourceType   string `json:"sourceType"`
			BootVolumeID string `json:"bootVolumeId"`
		} `json:"sourceDetails"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", relaunch), &reborn)
	assert.Equal(t, "bootVolume", reborn.SourceDetails.SourceType)
	assert.Equal(t, bootVolume, reborn.SourceDetails.BootVolumeID)

	noImage := h.launchBody("none")
	delete(noImage, "imageId")

	resp := h.do(http.MethodPost, "/20160918/instances", noImage)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, bodyOf(t, resp), "imageId or sourceDetails is required")

	noShape := h.launchBody("none")
	delete(noShape, "shape")

	resp = h.do(http.MethodPost, "/20160918/instances", noShape)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, bodyOf(t, resp), "shape is required")
}

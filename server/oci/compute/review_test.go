package compute_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// launchBody is a launch OCI accepts, for tests to vary one field of.
func (h *harness) launchBody(displayName string) map[string]any {
	return map[string]any{
		"compartmentId":      compartment,
		"availabilityDomain": testAD,
		"displayName":        displayName,
		"shape":              shape,
		"imageId":            h.image,
		"createVnicDetails":  map[string]any{"subnetId": h.subnet},
	}
}

// OCI leaves what an update omits untouched: a rename keeps the tags, and a
// tag change keeps the name.
func TestUpdateInstanceKeepsWhatItOmits(t *testing.T) {
	h := newHarness(t)
	id := h.launch("alpha")

	h.decode(h.do(http.MethodPut, "/20160918/instances/"+id,
		map[string]any{"freeformTags": map[string]string{"team": "x"}}), &struct{}{})

	var renamed struct {
		DisplayName  string            `json:"displayName"`
		FreeformTags map[string]string `json:"freeformTags"`
	}

	h.decode(h.do(http.MethodPut, "/20160918/instances/"+id, map[string]any{"displayName": "alpha2"}), &renamed)
	assert.Equal(t, "alpha2", renamed.DisplayName)
	assert.Equal(t, map[string]string{"team": "x"}, renamed.FreeformTags)

	var got struct {
		DisplayName  string            `json:"displayName"`
		FreeformTags map[string]string `json:"freeformTags"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/instances/"+id, nil), &got)
	assert.Equal(t, "alpha2", got.DisplayName)
	assert.Equal(t, map[string]string{"team": "x"}, got.FreeformTags)
}

func TestLaunchRefusesAnUnknownImage(t *testing.T) {
	h := newHarness(t)

	body := h.launchBody("ghost")
	body["imageId"] = "ocid1.image.oc1..bogus"

	resp := h.do(http.MethodPost, "/20160918/instances", body)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, bodyOf(t, resp))

	// The typed library path refuses the same launch.
	_, err := h.compute.RunInstances(t.Context(), driver.InstanceConfig{
		ImageID: "bogus", InstanceType: "Not.A.Shape", SubnetID: h.subnet,
	}, 1)
	require.Error(t, err)
}

func TestLaunchValidatesItsInputs(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name  string
		apply func(map[string]any)
	}{
		{"no availability domain", func(b map[string]any) { delete(b, "availabilityDomain") }},
		{"no subnet", func(b map[string]any) { delete(b, "createVnicDetails") }},
		{"ocpus above the shape's maximum", func(b map[string]any) {
			b["shapeConfig"] = map[string]any{"ocpus": 500}
		}},
		{"memory below the shape's minimum", func(b map[string]any) {
			b["shapeConfig"] = map[string]any{"memoryInGBs": 0.5}
		}},
		{"shapeConfig on a fixed shape", func(b map[string]any) {
			b["shape"] = "VM.Standard2.1"
			b["shapeConfig"] = map[string]any{"ocpus": 1}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := h.launchBody("bad")
			tc.apply(body)

			resp := h.do(http.MethodPost, "/20160918/instances", body)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, bodyOf(t, resp))
		})
	}

	ok := h.launchBody("flex")
	ok["shapeConfig"] = map[string]any{"ocpus": 4, "memoryInGBs": 64}
	h.decode(h.do(http.MethodPost, "/20160918/instances", ok), &struct{}{})
}

func TestCreatesAndMovesRequireAnExistingCompartment(t *testing.T) {
	h := newHarness(t)
	id := h.launch("mover")

	h.handler.SetCompartmentChecker(func(c string) bool { return c == compartment })

	const ghost = "ocid1.compartment.oc1..ghost"

	body := h.launchBody("ghost")
	body["compartmentId"] = ghost

	resp := h.do(http.MethodPost, "/20160918/instances", body)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, bodyOf(t, resp))

	resp = h.do(http.MethodPost, "/20160918/volumes", map[string]any{"compartmentId": ghost, "sizeInGBs": 50})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, bodyOf(t, resp))

	resp = h.do(http.MethodPost, "/20160918/instances/"+id+"/actions/changeCompartment",
		map[string]any{"compartmentId": ghost})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, bodyOf(t, resp))
}

// changeCompartment only moves a resource of the collection in its path.
func TestChangeCompartmentChecksTheCollection(t *testing.T) {
	h := newHarness(t)

	var vol struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumes",
		map[string]any{"compartmentId": compartment, "sizeInGBs": 50}), &vol)

	resp := h.do(http.MethodPost, "/20160918/instances/"+vol.ID+"/actions/changeCompartment",
		map[string]any{"compartmentId": "ocid1.compartment.oc1..other"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, bodyOf(t, resp))

	resp = h.do(http.MethodPost, "/20160918/volumes/"+vol.ID+"/actions/changeCompartment",
		map[string]any{"compartmentId": "ocid1.compartment.oc1..other"})
	assert.Equal(t, http.StatusAccepted, resp.StatusCode, bodyOf(t, resp))
}

func TestListInstancesFiltersAndSorts(t *testing.T) {
	h := newHarness(t)

	for _, name := range []string{"beta", "alpha", "gamma"} {
		h.launch(name)
	}

	names := func(query string) []string {
		t.Helper()

		resp := h.do(http.MethodGet, "/20160918/instances?compartmentId="+compartment+query, nil)

		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

		var items []struct {
			DisplayName string `json:"displayName"`
		}

		require.NoError(t, json.Unmarshal(raw, &items))

		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.DisplayName)
		}

		return out
	}

	assert.Equal(t, []string{"alpha"}, names("&displayName=alpha"))
	assert.Equal(t, []string{"gamma", "alpha", "beta"}, names(""), "TIMECREATED descending by default")
	assert.Equal(t, []string{"beta", "alpha", "gamma"}, names("&sortOrder=ASC"))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, names("&sortBy=DISPLAYNAME"))
	assert.Equal(t, []string{"gamma", "beta", "alpha"}, names("&sortBy=DISPLAYNAME&sortOrder=DESC"))

	resp := h.do(http.MethodGet, "/20160918/instances?compartmentId="+compartment+"&sortBy=SIZE", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// A clone or restore without a size takes its source's; a size below the
// source's, or outside OCI's 50-32768 GB, is refused.
func TestVolumeSizeFollowsItsSource(t *testing.T) {
	h := newHarness(t)

	var vol, backup, restored struct {
		ID        string `json:"id"`
		SizeInGBs int    `json:"sizeInGBs"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumes",
		map[string]any{"compartmentId": compartment, "sizeInGBs": 200}), &vol)
	h.decode(h.do(http.MethodPost, "/20160918/volumeBackups", map[string]any{"volumeId": vol.ID, "type": "FULL"}), &backup)

	h.decode(h.do(http.MethodPost, "/20160918/volumes", map[string]any{
		"compartmentId": compartment,
		"sourceDetails": map[string]any{"type": "volumeBackup", "id": backup.ID},
	}), &restored)
	assert.Equal(t, 200, restored.SizeInGBs)

	for name, body := range map[string]map[string]any{
		"smaller than the source": {
			"compartmentId": compartment, "sizeInGBs": 100,
			"sourceDetails": map[string]any{"type": "volume", "id": vol.ID},
		},
		"below the minimum": {"compartmentId": compartment, "sizeInGBs": 10},
		"above the maximum": {"compartmentId": compartment, "sizeInGBs": 999999},
	} {
		t.Run(name, func(t *testing.T) {
			resp := h.do(http.MethodPost, "/20160918/volumes", body)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, bodyOf(t, resp))
		})
	}
}

func TestVNICAttachmentSharesItsInstanceAvailabilityDomain(t *testing.T) {
	h := newHarness(t)

	body := h.launchBody("ad")
	body["availabilityDomain"] = "cloudemu:US-ASHBURN-1-AD-2"

	var inst struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", body), &inst)

	var attachments []struct {
		AvailabilityDomain string `json:"availabilityDomain"`
	}

	h.decodeList(h.do(http.MethodGet,
		"/20160918/vnicAttachments?compartmentId="+compartment+"&instanceId="+inst.ID, nil), &attachments)
	require.Len(t, attachments, 1)
	assert.Equal(t, "cloudemu:US-ASHBURN-1-AD-2", attachments[0].AvailabilityDomain)
}

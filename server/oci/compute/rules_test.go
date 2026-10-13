package compute_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateInstanceValidatesShapeConfigAgainstTheTargetShape(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	for name, body := range map[string]map[string]any{
		"ocpus beyond E4.Flex": {"shapeConfig": map[string]any{"ocpus": 500}},
		"memory per OCPU":      {"shapeConfig": map[string]any{"ocpus": 1, "memoryInGBs": 1000}},
		"config on a fixed shape": {
			"shape": "VM.Standard2.1", "shapeConfig": map[string]any{"ocpus": 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			resp := h.do(http.MethodPut, "/20160918/instances/"+id, body)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}

	var moved struct {
		Shape       string `json:"shape"`
		ShapeConfig *struct {
			Ocpus float32 `json:"ocpus"`
		} `json:"shapeConfig"`
	}

	h.decode(h.do(http.MethodPut, "/20160918/instances/"+id,
		map[string]any{"shapeConfig": map[string]any{"ocpus": 2, "memoryInGBs": 32}}), &struct{}{})
	h.decode(h.do(http.MethodPut, "/20160918/instances/"+id, map[string]any{"shape": "VM.Standard2.1"}), &moved)
	assert.Equal(t, "VM.Standard2.1", moved.Shape)
	assert.Nil(t, moved.ShapeConfig, "a fixed shape drops the flexible shape's config")
}

func TestLaunchChecksMemoryPerOCPU(t *testing.T) {
	h := newHarness(t)

	body := h.launchBody("big")
	body["shapeConfig"] = map[string]any{"ocpus": 1, "memoryInGBs": 1000}
	assert.Equal(t, http.StatusBadRequest, h.do(http.MethodPost, "/20160918/instances", body).StatusCode)

	body["shapeConfig"] = map[string]any{"ocpus": 16, "memoryInGBs": 1000}
	assert.Equal(t, http.StatusOK, h.do(http.MethodPost, "/20160918/instances", body).StatusCode)
}

func TestRelaunchFromAPreservedBootVolume(t *testing.T) {
	h := newHarness(t)
	first := h.launch("web-1")
	boot := h.bootVolumeOf(first)

	term := h.do(http.MethodDelete, "/20160918/instances/"+first+"?preserveBootVolume=true", nil)
	require.Equal(t, http.StatusNoContent, term.StatusCode)

	body := h.launchBody("web-2")
	delete(body, "imageId")
	body["sourceDetails"] = map[string]any{"sourceType": "bootVolume", "bootVolumeId": boot}

	var reborn struct {
		ID      string `json:"id"`
		ImageID string `json:"imageId"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", body), &reborn)
	assert.Equal(t, h.image, reborn.ImageID, "imageId is the boot volume's image, not the boot volume")

	var atts []struct {
		BootVolumeID string `json:"bootVolumeId"`
	}

	h.decodeList(h.do(http.MethodGet, "/20160918/bootVolumeAttachments?compartmentId="+compartment+
		"&instanceId="+reborn.ID, nil), &atts)
	require.Len(t, atts, 1)
	assert.Equal(t, boot, atts[0].BootVolumeID)

	again := h.do(http.MethodPost, "/20160918/instances", body)
	assert.Equal(t, http.StatusConflict, again.StatusCode, "a boot volume backs only one instance")
}

func TestLaunchSizesTheBootVolumeFromSourceDetails(t *testing.T) {
	h := newHarness(t)

	body := h.launchBody("sized")
	delete(body, "imageId")
	body["sourceDetails"] = map[string]any{"sourceType": "image", "imageId": h.image, "bootVolumeSizeInGBs": 100}

	var inst idOnly

	h.decode(h.do(http.MethodPost, "/20160918/instances", body), &inst)

	var bv struct {
		SizeInGBs int `json:"sizeInGBs"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumes/"+h.bootVolumeOf(inst.ID), nil), &bv)
	assert.Equal(t, 100, bv.SizeInGBs)

	body["sourceDetails"] = map[string]any{"sourceType": "image", "imageId": h.image, "bootVolumeSizeInGBs": 10}
	assert.Equal(t, http.StatusBadRequest, h.do(http.MethodPost, "/20160918/instances", body).StatusCode)
}

func TestVolumeUpdateReplacesFreeformTags(t *testing.T) {
	h := newHarness(t)

	var vol idOnly

	h.decode(h.do(http.MethodPost, "/20160918/volumes", map[string]any{
		"compartmentId": compartment, "sizeInGBs": 50, "displayName": "data",
		"freeformTags": map[string]string{"a": "1"},
	}), &vol)

	type volume struct {
		DisplayName  string            `json:"displayName"`
		FreeformTags map[string]string `json:"freeformTags"`
	}

	var replaced volume

	h.decode(h.do(http.MethodPut, "/20160918/volumes/"+vol.ID,
		map[string]any{"freeformTags": map[string]string{"b": "2"}}), &replaced)
	assert.Equal(t, map[string]string{"b": "2"}, replaced.FreeformTags)
	assert.Equal(t, "data", replaced.DisplayName)

	var renamed volume

	h.decode(h.do(http.MethodPut, "/20160918/volumes/"+vol.ID, map[string]any{"displayName": "data2"}), &renamed)
	assert.Equal(t, map[string]string{"b": "2"}, renamed.FreeformTags, "a rename leaves the tags alone")
}

func TestSharedVolumeCannotBeDeletedWhileAttached(t *testing.T) {
	h := newHarness(t)
	a, b := h.launch("a"), h.launch("b")
	vol := h.createVolume(50)

	attach := func(instanceID string) idOnly {
		var att idOnly

		h.decode(h.do(http.MethodPost, "/20160918/volumeAttachments", map[string]any{
			"type": "paravirtualized", "instanceId": instanceID, "volumeId": vol, "isShareable": true,
		}), &att)

		return att
	}

	first := attach(a)
	attach(b)

	require.Equal(t, http.StatusNoContent,
		h.do(http.MethodDelete, "/20160918/volumeAttachments/"+first.ID, nil).StatusCode)

	resp := h.do(http.MethodDelete, "/20160918/volumes/"+vol, nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode, "b still holds the volume")

	exclusive := h.do(http.MethodPost, "/20160918/volumeAttachments", map[string]any{
		"type": "paravirtualized", "instanceId": a, "volumeId": vol,
	})
	assert.Equal(t, http.StatusConflict, exclusive.StatusCode)
}

func TestISCSIAttachmentReportsItsTarget(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")

	var att struct {
		AttachmentType string `json:"attachmentType"`
		Ipv4           string `json:"ipv4"`
		Iqn            string `json:"iqn"`
		Port           int    `json:"port"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumeAttachments", map[string]any{
		"type": "iscsi", "instanceId": inst, "volumeId": h.createVolume(50),
	}), &att)
	assert.Equal(t, "iscsi", att.AttachmentType)
	assert.NotEmpty(t, att.Ipv4)
	assert.NotEmpty(t, att.Iqn)
	assert.Equal(t, 3260, att.Port)
}

func TestBootVolumeBackupMovesCompartment(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")

	var backup idOnly

	h.decode(h.do(http.MethodPost, "/20160918/bootVolumeBackups",
		map[string]any{"bootVolumeId": h.bootVolumeOf(inst)}), &backup)

	resp := h.do(http.MethodPost, "/20160918/bootVolumeBackups/"+backup.ID+"/actions/changeCompartment",
		map[string]any{"compartmentId": otherCompartment})
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	var got struct {
		CompartmentID string `json:"compartmentId"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumeBackups/"+backup.ID, nil), &got)
	assert.Equal(t, otherCompartment, got.CompartmentID)
}

func TestInstancePoolSoftstop(t *testing.T) {
	h := newHarness(t)
	cfg := h.instanceConfig(compartment, nil)

	var pool idOnly

	h.decode(h.do(http.MethodPost, "/20160918/instancePools", map[string]any{
		"compartmentId": compartment, "instanceConfigurationId": cfg, "size": 1,
		"placementConfigurations": []map[string]any{{"availabilityDomain": testAD, "primarySubnetId": h.subnet}},
	}), &pool)

	var stopped struct {
		LifecycleState string `json:"lifecycleState"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instancePools/"+pool.ID+"/actions/softstop", nil), &stopped)
	assert.Equal(t, "STOPPED", stopped.LifecycleState)
}

// instanceConfig saves a launch into compartmentID, with any extra launch
// details merged in.
func (h *harness) instanceConfig(compartmentID string, extra map[string]any) string {
	h.t.Helper()

	launch := map[string]any{
		"availabilityDomain": testAD,
		"shape":              shape,
		"sourceDetails":      map[string]any{"sourceType": "image", "imageId": h.image},
		"createVnicDetails":  map[string]any{"subnetId": h.subnet},
	}

	for k, v := range extra {
		launch[k] = v
	}

	var cfg idOnly

	h.decode(h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{
		"compartmentId":   compartmentID,
		"instanceDetails": map[string]any{"instanceType": "compute", "launchDetails": launch},
	}), &cfg)

	return cfg.ID
}

func TestLaunchFromConfigurationLandsInTheLaunchCompartment(t *testing.T) {
	h := newHarness(t)
	h.handler.SetCompartmentChecker(func(id string) bool { return id == compartment || id == otherCompartment })

	listed := func(collection, instanceID, compartmentID string) int {
		var items []idOnly

		h.decodeList(h.do(http.MethodGet, "/20160918/"+collection+"?compartmentId="+compartmentID+
			"&instanceId="+instanceID, nil), &items)

		return len(items)
	}

	launch := func(cfg string, body map[string]any) *http.Response {
		return h.do(http.MethodPost, "/20160918/instanceConfigurations/"+cfg+"/actions/launch", body)
	}

	t.Run("the configuration's compartment", func(t *testing.T) {
		var inst struct {
			ID            string `json:"id"`
			CompartmentID string `json:"compartmentId"`
		}

		h.decode(launch(h.instanceConfig(otherCompartment, nil), nil), &inst)
		assert.Equal(t, otherCompartment, inst.CompartmentID)
		assert.Equal(t, 1, listed("vnicAttachments", inst.ID, otherCompartment))
		assert.Equal(t, 1, listed("bootVolumeAttachments", inst.ID, otherCompartment))
	})

	t.Run("the saved launch compartment", func(t *testing.T) {
		var inst struct {
			CompartmentID string `json:"compartmentId"`
		}

		cfg := h.instanceConfig(compartment, map[string]any{"compartmentId": otherCompartment})
		h.decode(launch(cfg, nil), &inst)
		assert.Equal(t, otherCompartment, inst.CompartmentID)
	})

	t.Run("the override compartment", func(t *testing.T) {
		var inst struct {
			CompartmentID string `json:"compartmentId"`
		}

		cfg := h.instanceConfig(compartment, nil)
		h.decode(launch(cfg, map[string]any{
			"instanceType": "compute", "launchDetails": map[string]any{"compartmentId": otherCompartment},
		}), &inst)
		assert.Equal(t, otherCompartment, inst.CompartmentID)

		ghost := launch(cfg, map[string]any{
			"instanceType": "compute", "launchDetails": map[string]any{"compartmentId": "ocid1.compartment.oc1..ghost"},
		})
		assert.Equal(t, http.StatusNotFound, ghost.StatusCode)
	})
}

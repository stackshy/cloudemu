package compute_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type idOnly struct {
	ID string `json:"id"`
}

// createVolume creates a block volume of the given size and returns its OCID.
func (h *harness) createVolume(sizeInGBs int) string {
	h.t.Helper()

	var vol idOnly

	h.decode(h.do(http.MethodPost, "/20160918/volumes",
		map[string]any{"compartmentId": compartment, "sizeInGBs": sizeInGBs}), &vol)

	return vol.ID
}

func (h *harness) bootVolumeOf(instanceID string) string {
	h.t.Helper()

	d, ok := h.compute.InstanceDetails(instanceID)
	require.True(h.t, ok)

	return d.BootVolumeID
}

func TestBootVolumeCRUD(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")
	source := h.bootVolumeOf(inst)

	type bootVolume struct {
		ID            string            `json:"id"`
		CompartmentID string            `json:"compartmentId"`
		DisplayName   string            `json:"displayName"`
		SizeInGBs     int               `json:"sizeInGBs"`
		VpusPerGB     int               `json:"vpusPerGB"`
		FreeformTags  map[string]string `json:"freeformTags"`
		SourceDetails struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"sourceDetails"`
		LifecycleState string `json:"lifecycleState"`
	}

	resp := h.do(http.MethodPost, "/20160918/bootVolumes", map[string]any{
		"compartmentId": compartment, "displayName": "clone", "sizeInGBs": 60,
		"sourceDetails": map[string]any{"type": "bootVolume", "id": source},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"))

	var clone bootVolume

	h.decode(resp, &clone)
	assert.Equal(t, "clone", clone.DisplayName)
	assert.Equal(t, 60, clone.SizeInGBs)
	assert.Equal(t, "bootVolume", clone.SourceDetails.Type)
	assert.Equal(t, source, clone.SourceDetails.ID)
	assert.Equal(t, "AVAILABLE", clone.LifecycleState)

	var got bootVolume

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumes/"+clone.ID, nil), &got)
	assert.Equal(t, clone.ID, got.ID)
	assert.Equal(t, compartment, got.CompartmentID)

	var updated bootVolume

	h.decode(h.do(http.MethodPut, "/20160918/bootVolumes/"+clone.ID, map[string]any{
		"displayName": "renamed", "sizeInGBs": 100, "vpusPerGB": 20,
		"freeformTags": map[string]string{"env": "dev"},
	}), &updated)
	assert.Equal(t, "renamed", updated.DisplayName)
	assert.Equal(t, 100, updated.SizeInGBs)
	assert.Equal(t, 20, updated.VpusPerGB)
	assert.Equal(t, map[string]string{"env": "dev"}, updated.FreeformTags)

	shrink := h.do(http.MethodPut, "/20160918/bootVolumes/"+clone.ID, map[string]any{"sizeInGBs": 50})
	assert.Equal(t, http.StatusBadRequest, shrink.StatusCode)

	var listed []bootVolume

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumes?compartmentId="+compartment, nil), &listed)
	assert.Len(t, listed, 2, "the launch's boot volume and the clone")

	unknown := h.do(http.MethodPost, "/20160918/bootVolumes", map[string]any{
		"compartmentId": compartment,
		"sourceDetails": map[string]any{"type": "bootVolume", "id": "ocid1.bootvolume.oc1.iad.missing"},
	})
	assert.Equal(t, http.StatusNotFound, unknown.StatusCode)

	attached := h.do(http.MethodDelete, "/20160918/bootVolumes/"+source, nil)
	assert.Equal(t, http.StatusConflict, attached.StatusCode, "an attached boot volume is not deleted")

	del := h.do(http.MethodDelete, "/20160918/bootVolumes/"+clone.ID, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotEmpty(t, del.Header.Get("opc-work-request-id"))

	gone := h.do(http.MethodGet, "/20160918/bootVolumes/"+clone.ID, nil)
	assert.Equal(t, http.StatusNotFound, gone.StatusCode)
}

func TestBootVolumeAttachmentCRUD(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")
	original := h.bootVolumeOf(inst)

	var replacement idOnly

	h.decode(h.do(http.MethodPost, "/20160918/bootVolumes", map[string]any{
		"compartmentId": compartment,
		"sourceDetails": map[string]any{"type": "bootVolume", "id": original},
	}), &replacement)

	type attachment struct {
		ID             string `json:"id"`
		InstanceID     string `json:"instanceId"`
		BootVolumeID   string `json:"bootVolumeId"`
		CompartmentID  string `json:"compartmentId"`
		DisplayName    string `json:"displayName"`
		LifecycleState string `json:"lifecycleState"`
	}

	running := h.do(http.MethodPost, "/20160918/bootVolumeAttachments",
		map[string]any{"instanceId": inst, "bootVolumeId": replacement.ID})
	assert.Equal(t, http.StatusConflict, running.StatusCode, "a boot volume is swapped only on a stopped instance")

	h.decode(h.do(http.MethodPost, "/20160918/instances/"+inst+"?action=STOP", nil), &struct{}{})

	busy := h.do(http.MethodPost, "/20160918/bootVolumeAttachments",
		map[string]any{"instanceId": inst, "bootVolumeId": replacement.ID})
	assert.Equal(t, http.StatusConflict, busy.StatusCode, "the instance already boots from a volume")

	var launched []attachment

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumeAttachments?compartmentId="+compartment+
		"&instanceId="+inst+"&bootVolumeId="+original, nil), &launched)
	require.Len(t, launched, 1)

	var got attachment

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumeAttachments/"+launched[0].ID, nil), &got)
	assert.Equal(t, inst, got.InstanceID)
	assert.Equal(t, original, got.BootVolumeID)
	assert.Equal(t, "ATTACHED", got.LifecycleState)

	detach := h.do(http.MethodDelete, "/20160918/bootVolumeAttachments/"+launched[0].ID, nil)
	assert.Equal(t, http.StatusNoContent, detach.StatusCode)
	assert.NotEmpty(t, detach.Header.Get("opc-work-request-id"))

	gone := h.do(http.MethodGet, "/20160918/bootVolumeAttachments/"+launched[0].ID, nil)
	assert.Equal(t, http.StatusNotFound, gone.StatusCode)

	resp := h.do(http.MethodPost, "/20160918/bootVolumeAttachments", map[string]any{
		"instanceId": inst, "bootVolumeId": replacement.ID, "displayName": "swap",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"))

	var att attachment

	h.decode(resp, &att)
	assert.Equal(t, replacement.ID, att.BootVolumeID)
	assert.Equal(t, "swap", att.DisplayName)
	assert.Equal(t, compartment, att.CompartmentID)

	update := h.do(http.MethodPut, "/20160918/bootVolumeAttachments/"+att.ID, map[string]any{})
	assert.Equal(t, http.StatusMethodNotAllowed, update.StatusCode)

	missing := h.do(http.MethodPost, "/20160918/bootVolumeAttachments", map[string]any{"instanceId": inst})
	assert.Equal(t, http.StatusBadRequest, missing.StatusCode)
	assert.Contains(t, bodyOf(t, missing), "instanceId and bootVolumeId are required")

	// The detached volume is free to go.
	del := h.do(http.MethodDelete, "/20160918/bootVolumes/"+original, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
}

func TestBackupUpdateAndDelete(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")
	vol := h.createVolume(50)

	type backup struct {
		ID             string            `json:"id"`
		DisplayName    string            `json:"displayName"`
		Type           string            `json:"type"`
		SizeInGBs      int               `json:"sizeInGBs"`
		BootVolumeID   string            `json:"bootVolumeId"`
		FreeformTags   map[string]string `json:"freeformTags"`
		LifecycleState string            `json:"lifecycleState"`
	}

	var created backup

	h.decode(h.do(http.MethodPost, "/20160918/volumeBackups",
		map[string]any{"volumeId": vol, "displayName": "nightly", "type": "FULL"}), &created)
	assert.Equal(t, "AVAILABLE", created.LifecycleState)
	assert.Equal(t, 50, created.SizeInGBs)

	var got backup

	h.decode(h.do(http.MethodGet, "/20160918/volumeBackups/"+created.ID, nil), &got)
	assert.Equal(t, "nightly", got.DisplayName)
	assert.Equal(t, "FULL", got.Type)

	var updated backup

	h.decode(h.do(http.MethodPut, "/20160918/volumeBackups/"+created.ID, map[string]any{
		"displayName": "weekly", "freeformTags": map[string]string{"keep": "yes"},
	}), &updated)
	assert.Equal(t, "weekly", updated.DisplayName)
	assert.Equal(t, map[string]string{"keep": "yes"}, updated.FreeformTags)

	var boot backup

	h.decode(h.do(http.MethodPost, "/20160918/bootVolumeBackups",
		map[string]any{"bootVolumeId": h.bootVolumeOf(inst)}), &boot)
	assert.Equal(t, "INCREMENTAL", boot.Type, "the type defaults to INCREMENTAL")

	var bootListed []backup

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumeBackups?compartmentId="+compartment, nil), &bootListed)
	require.Len(t, bootListed, 1)
	assert.Equal(t, boot.ID, bootListed[0].ID)

	var bootGot backup

	h.decode(h.do(http.MethodGet, "/20160918/bootVolumeBackups/"+boot.ID, nil), &bootGot)
	assert.Equal(t, h.bootVolumeOf(inst), bootGot.BootVolumeID)

	missingBoot := h.do(http.MethodPost, "/20160918/bootVolumeBackups", map[string]any{"displayName": "x"})
	assert.Equal(t, http.StatusBadRequest, missingBoot.StatusCode)
	assert.Contains(t, bodyOf(t, missingBoot), "bootVolumeId is required")

	badType := h.do(http.MethodPost, "/20160918/volumeBackups", map[string]any{"volumeId": vol, "type": "ARCHIVE"})
	assert.Equal(t, http.StatusBadRequest, badType.StatusCode)

	wrongCollection := h.do(http.MethodGet, "/20160918/bootVolumeBackups/"+created.ID, nil)
	assert.Equal(t, http.StatusNotFound, wrongCollection.StatusCode)

	del := h.do(http.MethodDelete, "/20160918/volumeBackups/"+created.ID, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotEmpty(t, del.Header.Get("opc-work-request-id"))

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp := h.do(method, "/20160918/volumeBackups/"+created.ID, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
	}

	resp := h.do(http.MethodPut, "/20160918/volumeBackups/"+created.ID, map[string]any{"displayName": "x"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestImageGetUpdateDelete(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")

	type image struct {
		ID              string            `json:"id"`
		DisplayName     string            `json:"displayName"`
		OperatingSystem string            `json:"operatingSystem"`
		FreeformTags    map[string]string `json:"freeformTags"`
	}

	var created image

	h.decode(h.do(http.MethodPost, "/20160918/images", map[string]any{
		"compartmentId": compartment, "instanceId": inst, "displayName": "golden",
	}), &created)

	var got image

	h.decode(h.do(http.MethodGet, "/20160918/images/"+created.ID, nil), &got)
	assert.Equal(t, "golden", got.DisplayName)

	var updated image

	h.decode(h.do(http.MethodPut, "/20160918/images/"+created.ID, map[string]any{
		"displayName": "golden-v2", "freeformTags": map[string]string{"tier": "web"},
	}), &updated)
	assert.Equal(t, "golden-v2", updated.DisplayName)
	assert.Equal(t, map[string]string{"tier": "web"}, updated.FreeformTags)

	platform := h.do(http.MethodPut, "/20160918/images/"+h.image, map[string]any{"displayName": "mine"})
	assert.Equal(t, http.StatusConflict, platform.StatusCode, "a platform image is read-only")

	var oracle []image

	h.decode(h.do(http.MethodGet,
		"/20160918/images?compartmentId="+compartment+"&operatingSystem=Oracle%20Linux", nil), &oracle)
	require.NotEmpty(t, oracle)

	for i := range oracle {
		assert.Equal(t, "Oracle Linux", oracle[i].OperatingSystem)
	}

	imported := h.do(http.MethodPost, "/20160918/images", map[string]any{
		"compartmentId":      compartment,
		"imageSourceDetails": map[string]any{"sourceType": "objectStorageUri"},
	})
	assert.Equal(t, http.StatusNotImplemented, imported.StatusCode)

	noCompartment := h.do(http.MethodPost, "/20160918/images", map[string]any{"instanceId": inst})
	assert.Equal(t, http.StatusBadRequest, noCompartment.StatusCode)

	unknownInstance := h.do(http.MethodPost, "/20160918/images", map[string]any{
		"compartmentId": compartment, "instanceId": "ocid1.instance.oc1.iad.missing",
	})
	assert.Equal(t, http.StatusNotFound, unknownInstance.StatusCode)

	del := h.do(http.MethodDelete, "/20160918/images/"+created.ID, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotEmpty(t, del.Header.Get("opc-work-request-id"))

	gone := h.do(http.MethodGet, "/20160918/images/"+created.ID, nil)
	assert.Equal(t, http.StatusNotFound, gone.StatusCode)

	platformDelete := h.do(http.MethodDelete, "/20160918/images/"+h.image, nil)
	assert.Equal(t, http.StatusConflict, platformDelete.StatusCode)
}

func TestVolumeGetUpdateDelete(t *testing.T) {
	h := newHarness(t)

	type volume struct {
		ID           string            `json:"id"`
		DisplayName  string            `json:"displayName"`
		SizeInGBs    int               `json:"sizeInGBs"`
		SizeInMBs    int               `json:"sizeInMBs"`
		VpusPerGB    int               `json:"vpusPerGB"`
		FreeformTags map[string]string `json:"freeformTags"`
	}

	var created volume

	h.decode(h.do(http.MethodPost, "/20160918/volumes", map[string]any{
		"compartmentId": compartment, "displayName": "data", "sizeInMBs": 51200,
	}), &created)
	assert.Equal(t, 50, created.SizeInGBs, "sizeInMBs is read when sizeInGBs is absent")
	assert.Equal(t, 51200, created.SizeInMBs)

	var updated volume

	h.decode(h.do(http.MethodPut, "/20160918/volumes/"+created.ID, map[string]any{
		"displayName": "data2", "sizeInGBs": 200, "vpusPerGB": 20,
		"freeformTags": map[string]string{"env": "prod"},
	}), &updated)
	assert.Equal(t, "data2", updated.DisplayName)
	assert.Equal(t, 200, updated.SizeInGBs)
	assert.Equal(t, 20, updated.VpusPerGB)
	assert.Equal(t, map[string]string{"env": "prod"}, updated.FreeformTags)

	var got volume

	h.decode(h.do(http.MethodGet, "/20160918/volumes/"+created.ID, nil), &got)
	assert.Equal(t, updated, got)

	shrink := h.do(http.MethodPut, "/20160918/volumes/"+created.ID, map[string]any{"sizeInGBs": 100})
	assert.Equal(t, http.StatusBadRequest, shrink.StatusCode)

	unknown := h.do(http.MethodPut, "/20160918/volumes/ocid1.volume.oc1.iad.missing",
		map[string]any{"displayName": "x"})
	assert.Equal(t, http.StatusNotFound, unknown.StatusCode)

	var mine, theirs []volume

	h.decode(h.do(http.MethodGet, "/20160918/volumes?compartmentId="+compartment, nil), &mine)
	require.Len(t, mine, 1)
	h.decode(h.do(http.MethodGet, "/20160918/volumes?compartmentId="+otherCompartment, nil), &theirs)
	assert.Empty(t, theirs)

	del := h.do(http.MethodDelete, "/20160918/volumes/"+created.ID, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotEmpty(t, del.Header.Get("opc-work-request-id"))

	gone := h.do(http.MethodDelete, "/20160918/volumes/"+created.ID, nil)
	assert.Equal(t, http.StatusNotFound, gone.StatusCode)
}

func TestVolumeAttachmentGetAndDetach(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")
	vol := h.createVolume(50)

	type attachment struct {
		ID                             string `json:"id"`
		InstanceID                     string `json:"instanceId"`
		VolumeID                       string `json:"volumeId"`
		AttachmentType                 string `json:"attachmentType"`
		IsReadOnly                     bool   `json:"isReadOnly"`
		IsPvEncryptionInTransitEnabled bool   `json:"isPvEncryptionInTransitEnabled"`
		LifecycleState                 string `json:"lifecycleState"`
	}

	var att attachment

	h.decode(h.do(http.MethodPost, "/20160918/volumeAttachments", map[string]any{
		"type": "iscsi", "instanceId": inst, "volumeId": vol, "isReadOnly": true,
		"isPvEncryptionInTransitEnabled": true,
	}), &att)
	assert.Equal(t, "iscsi", att.AttachmentType)
	assert.True(t, att.IsReadOnly)
	assert.True(t, att.IsPvEncryptionInTransitEnabled)

	var got attachment

	h.decode(h.do(http.MethodGet, "/20160918/volumeAttachments/"+att.ID, nil), &got)
	assert.Equal(t, att, got)

	var byVolume []attachment

	h.decode(h.do(http.MethodGet,
		"/20160918/volumeAttachments?compartmentId="+compartment+"&volumeId="+vol, nil), &byVolume)
	require.Len(t, byVolume, 1)
	assert.Equal(t, att.ID, byVolume[0].ID)

	inUse := h.do(http.MethodDelete, "/20160918/volumes/"+vol, nil)
	assert.Equal(t, http.StatusConflict, inUse.StatusCode, "an attached volume is not deleted")

	detach := h.do(http.MethodDelete, "/20160918/volumeAttachments/"+att.ID, nil)
	assert.Equal(t, http.StatusNoContent, detach.StatusCode)
	assert.NotEmpty(t, detach.Header.Get("opc-work-request-id"))

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp := h.do(method, "/20160918/volumeAttachments/"+att.ID, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
	}

	var after struct {
		LifecycleState string `json:"lifecycleState"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/volumes/"+vol, nil), &after)
	assert.Equal(t, "AVAILABLE", after.LifecycleState)
}

func TestVolumeGroupCRUD(t *testing.T) {
	h := newHarness(t)
	first := h.createVolume(50)
	second := h.createVolume(100)
	third := h.createVolume(60)

	type group struct {
		ID           string            `json:"id"`
		DisplayName  string            `json:"displayName"`
		SizeInGBs    int               `json:"sizeInGBs"`
		SizeInMBs    int               `json:"sizeInMBs"`
		VolumeIDs    []string          `json:"volumeIds"`
		FreeformTags map[string]string `json:"freeformTags"`
	}

	resp := h.do(http.MethodPost, "/20160918/volumeGroups", map[string]any{
		"compartmentId": compartment, "displayName": "cg",
		"sourceDetails": map[string]any{"type": "volumeIds", "volumeIds": []string{first, second}},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"))

	var created group

	h.decode(resp, &created)
	assert.Equal(t, 150, created.SizeInGBs)
	assert.Equal(t, 150*1024, created.SizeInMBs)
	assert.Equal(t, []string{first, second}, created.VolumeIDs)

	var got group

	h.decode(h.do(http.MethodGet, "/20160918/volumeGroups/"+created.ID, nil), &got)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "cg", got.DisplayName)

	var updated group

	h.decode(h.do(http.MethodPut, "/20160918/volumeGroups/"+created.ID, map[string]any{
		"displayName": "cg2", "volumeIds": []string{first, third},
		"freeformTags": map[string]string{"app": "db"},
	}), &updated)
	assert.Equal(t, "cg2", updated.DisplayName)
	assert.Equal(t, 110, updated.SizeInGBs)
	assert.Equal(t, []string{first, third}, updated.VolumeIDs)
	assert.Equal(t, map[string]string{"app": "db"}, updated.FreeformTags)

	badMember := h.do(http.MethodPut, "/20160918/volumeGroups/"+created.ID,
		map[string]any{"volumeIds": []string{"ocid1.volume.oc1.iad.missing"}})
	assert.Equal(t, http.StatusNotFound, badMember.StatusCode)

	var cloned group

	h.decode(h.do(http.MethodPost, "/20160918/volumeGroups", map[string]any{
		"compartmentId": compartment,
		"sourceDetails": map[string]any{"type": "volumeGroupId", "volumeGroupId": created.ID},
	}), &cloned)
	assert.Equal(t, []string{first, third}, cloned.VolumeIDs)

	var plain group

	h.decode(h.do(http.MethodPost, "/20160918/volumeGroups", map[string]any{
		"compartmentId": compartment, "volumeIds": []string{second},
	}), &plain)
	assert.Equal(t, 100, plain.SizeInGBs, "top-level volumeIds work without sourceDetails")

	var listed []group

	h.decode(h.do(http.MethodGet, "/20160918/volumeGroups?compartmentId="+compartment, nil), &listed)
	assert.Len(t, listed, 3)

	empty := h.do(http.MethodPost, "/20160918/volumeGroups", map[string]any{"compartmentId": compartment})
	assert.Equal(t, http.StatusBadRequest, empty.StatusCode)

	noCompartment := h.do(http.MethodPost, "/20160918/volumeGroups", map[string]any{"volumeIds": []string{first}})
	assert.Equal(t, http.StatusBadRequest, noCompartment.StatusCode)

	del := h.do(http.MethodDelete, "/20160918/volumeGroups/"+created.ID, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotEmpty(t, del.Header.Get("opc-work-request-id"))

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp := h.do(method, "/20160918/volumeGroups/"+created.ID, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
	}
}

func TestVNICAttachmentAttachGetDetach(t *testing.T) {
	h := newHarness(t)
	inst := h.launch("web-1")

	var subnet idOnly

	h.decode(h.do(http.MethodPost, "/20160918/subnets", map[string]any{
		"compartmentId": compartment, "vcnId": h.vcnID, "cidrBlock": "10.0.2.0/24",
	}), &subnet)

	type attachment struct {
		ID            string `json:"id"`
		InstanceID    string `json:"instanceId"`
		SubnetID      string `json:"subnetId"`
		VnicID        string `json:"vnicId"`
		DisplayName   string `json:"displayName"`
		NicIndex      int    `json:"nicIndex"`
		CompartmentID string `json:"compartmentId"`
	}

	resp := h.do(http.MethodPost, "/20160918/vnicAttachments", map[string]any{
		"instanceId": inst,
		"createVnicDetails": map[string]any{
			"subnetId": subnet.ID, "displayName": "secondary", "hostnameLabel": "web1b",
		},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"))

	var att attachment

	h.decode(resp, &att)
	assert.Equal(t, subnet.ID, att.SubnetID)
	assert.Equal(t, "secondary", att.DisplayName, "the name falls back to createVnicDetails'")
	assert.Equal(t, 1, att.NicIndex)
	assert.Equal(t, compartment, att.CompartmentID)

	var got attachment

	h.decode(h.do(http.MethodGet, "/20160918/vnicAttachments/"+att.ID, nil), &got)
	assert.Equal(t, att, got)

	var all []attachment

	h.decode(h.do(http.MethodGet,
		"/20160918/vnicAttachments?compartmentId="+compartment+"&instanceId="+inst, nil), &all)
	require.Len(t, all, 2)

	var primary attachment

	for i := range all {
		if all[i].ID != att.ID {
			primary = all[i]
		}
	}

	primaryDetach := h.do(http.MethodDelete, "/20160918/vnicAttachments/"+primary.ID, nil)
	assert.Equal(t, http.StatusConflict, primaryDetach.StatusCode, "the primary VNIC goes with the instance")

	detach := h.do(http.MethodDelete, "/20160918/vnicAttachments/"+att.ID, nil)
	assert.Equal(t, http.StatusNoContent, detach.StatusCode)
	assert.NotEmpty(t, detach.Header.Get("opc-work-request-id"))

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp := h.do(method, "/20160918/vnicAttachments/"+att.ID, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
	}

	for name, body := range map[string]map[string]any{
		"no subnet":       {"instanceId": inst, "createVnicDetails": map[string]any{}},
		"no vnic details": {"instanceId": inst},
	} {
		t.Run(name, func(t *testing.T) {
			resp := h.do(http.MethodPost, "/20160918/vnicAttachments", body)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}

	unknown := h.do(http.MethodPost, "/20160918/vnicAttachments", map[string]any{
		"instanceId":        "ocid1.instance.oc1.iad.missing",
		"createVnicDetails": map[string]any{"subnetId": subnet.ID},
	})
	assert.Equal(t, http.StatusNotFound, unknown.StatusCode)
}

func TestInstanceConfigurationCRUD(t *testing.T) {
	h := newHarness(t)

	type config struct {
		ID              string            `json:"id"`
		DisplayName     string            `json:"displayName"`
		InstanceType    string            `json:"instanceType"`
		FreeformTags    map[string]string `json:"freeformTags"`
		InstanceDetails struct {
			LaunchDetails struct {
				Shape         string `json:"shape"`
				SourceDetails struct {
					ImageID string `json:"imageId"`
				} `json:"sourceDetails"`
				CreateVnicDetails struct {
					SubnetID string `json:"subnetId"`
				} `json:"createVnicDetails"`
				PreemptibleInstanceConfig *struct {
					PreemptionAction struct {
						Type string `json:"type"`
					} `json:"preemptionAction"`
				} `json:"preemptibleInstanceConfig"`
			} `json:"launchDetails"`
		} `json:"instanceDetails"`
	}

	var created config

	h.decode(h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{
		"compartmentId": compartment,
		"displayName":   "tpl",
		"instanceDetails": map[string]any{
			"instanceType": "compute",
			"launchDetails": map[string]any{
				"availabilityDomain": testAD,
				"shape":              shape,
				"shapeConfig":        map[string]any{"ocpus": 2},
				"sourceDetails":      map[string]any{"sourceType": "image", "imageId": h.image},
				"createVnicDetails":  map[string]any{"subnetId": h.subnet},
				"preemptibleInstanceConfig": map[string]any{
					"preemptionAction": map[string]any{"type": "TERMINATE"},
				},
			},
		},
	}), &created)
	assert.Equal(t, "compute", created.InstanceType)
	assert.Equal(t, shape, created.InstanceDetails.LaunchDetails.Shape)
	assert.Equal(t, h.image, created.InstanceDetails.LaunchDetails.SourceDetails.ImageID)
	assert.Equal(t, h.subnet, created.InstanceDetails.LaunchDetails.CreateVnicDetails.SubnetID)
	require.NotNil(t, created.InstanceDetails.LaunchDetails.PreemptibleInstanceConfig)

	var got config

	h.decode(h.do(http.MethodGet, "/20160918/instanceConfigurations/"+created.ID, nil), &got)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "tpl", got.DisplayName)

	var updated config

	h.decode(h.do(http.MethodPut, "/20160918/instanceConfigurations/"+created.ID, map[string]any{
		"displayName": "tpl2", "freeformTags": map[string]string{"v": "2"},
	}), &updated)
	assert.Equal(t, "tpl2", updated.DisplayName)
	assert.Equal(t, map[string]string{"v": "2"}, updated.FreeformTags)

	immutable := h.do(http.MethodPut, "/20160918/instanceConfigurations/"+created.ID, map[string]any{
		"instanceDetails": map[string]any{"instanceType": "compute"},
	})
	assert.Equal(t, http.StatusBadRequest, immutable.StatusCode)

	var listed []config

	h.decode(h.do(http.MethodGet, "/20160918/instanceConfigurations?compartmentId="+compartment, nil), &listed)
	require.Len(t, listed, 1)
	assert.Equal(t, created.ID, listed[0].ID)

	var launched struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instanceConfigurations/"+created.ID+"/actions/launch",
		map[string]any{"launchDetails": map[string]any{"shape": shape, "displayName": "override"}}), &launched)
	assert.Equal(t, "override", launched.DisplayName)

	malformed := h.doRaw(http.MethodPost, "/20160918/instanceConfigurations/"+created.ID+"/actions/launch", "{")
	assert.Equal(t, http.StatusBadRequest, malformed.StatusCode)

	get := h.do(http.MethodGet, "/20160918/instanceConfigurations/"+created.ID+"/actions/launch", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, get.StatusCode)

	other := h.do(http.MethodPost, "/20160918/instanceConfigurations/"+created.ID+"/actions/launchStack", nil)
	assert.Equal(t, http.StatusNotImplemented, other.StatusCode)

	missing := h.do(http.MethodPost,
		"/20160918/instanceConfigurations/ocid1.instanceconfiguration.oc1.iad.missing/actions/launch", nil)
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)

	unemulatedType := h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{
		"compartmentId": compartment,
		"instanceDetails": map[string]any{
			"instanceType": "instance_options", "launchDetails": map[string]any{"shape": shape},
		},
	})
	assert.Equal(t, http.StatusNotImplemented, unemulatedType.StatusCode)

	noShape := h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{
		"compartmentId": compartment, "instanceDetails": map[string]any{"launchDetails": map[string]any{}},
	})
	assert.Equal(t, http.StatusBadRequest, noShape.StatusCode)

	noCompartment := h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, noCompartment.StatusCode)

	del := h.do(http.MethodDelete, "/20160918/instanceConfigurations/"+created.ID, nil)
	assert.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotEmpty(t, del.Header.Get("opc-work-request-id"))

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp := h.do(method, "/20160918/instanceConfigurations/"+created.ID, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, method)
	}

	resp := h.do(http.MethodPut, "/20160918/instanceConfigurations/"+created.ID, map[string]any{"displayName": "x"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestInstancePoolCRUD(t *testing.T) {
	h := newHarness(t)

	var cfg idOnly

	h.decode(h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{
		"compartmentId": compartment,
		"instanceDetails": map[string]any{
			"launchDetails": map[string]any{
				"shape":             shape,
				"sourceDetails":     map[string]any{"sourceType": "image", "imageId": h.image},
				"createVnicDetails": map[string]any{"subnetId": h.subnet},
			},
		},
	}), &cfg)

	type pool struct {
		ID             string            `json:"id"`
		DisplayName    string            `json:"displayName"`
		Size           int               `json:"size"`
		LifecycleState string            `json:"lifecycleState"`
		FreeformTags   map[string]string `json:"freeformTags"`
		Placements     []struct {
			AvailabilityDomain string `json:"availabilityDomain"`
			PrimarySubnetID    string `json:"primarySubnetId"`
		} `json:"placementConfigurations"`
	}

	var created pool

	h.decode(h.do(http.MethodPost, "/20160918/instancePools", map[string]any{
		"compartmentId": compartment, "displayName": "web", "instanceConfigurationId": cfg.ID, "size": 1,
		"placementConfigurations": []map[string]any{{"availabilityDomain": testAD, "primarySubnetId": h.subnet}},
	}), &created)
	require.Len(t, created.Placements, 1)
	assert.Equal(t, testAD, created.Placements[0].AvailabilityDomain)
	assert.Equal(t, h.subnet, created.Placements[0].PrimarySubnetID)

	var got pool

	h.decode(h.do(http.MethodGet, "/20160918/instancePools/"+created.ID, nil), &got)
	assert.Equal(t, "web", got.DisplayName)
	assert.Equal(t, 1, got.Size)

	resp := h.do(http.MethodPut, "/20160918/instancePools/"+created.ID, map[string]any{
		"displayName": "web2", "size": 2, "freeformTags": map[string]string{"tier": "front"},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"))

	var updated pool

	h.decode(resp, &updated)
	assert.Equal(t, "web2", updated.DisplayName)
	assert.Equal(t, 2, updated.Size)
	assert.Equal(t, map[string]string{"tier": "front"}, updated.FreeformTags)

	var members []struct {
		InstanceID    string `json:"instanceId"`
		CompartmentID string `json:"compartmentId"`
	}

	h.decode(h.do(http.MethodGet,
		"/20160918/instancePools/"+created.ID+"/instances?compartmentId="+compartment, nil), &members)
	require.Len(t, members, 2)

	for i := range members {
		assert.Equal(t, compartment, members[i].CompartmentID, "a grown pool places its new members")
	}

	tooBig := h.do(http.MethodPut, "/20160918/instancePools/"+created.ID, map[string]any{"size": 5000})
	assert.Equal(t, http.StatusBadRequest, tooBig.StatusCode)

	var listed []pool

	h.decode(h.do(http.MethodGet, "/20160918/instancePools?compartmentId="+compartment, nil), &listed)
	require.Len(t, listed, 1)

	for _, step := range []struct{ action, want string }{
		{"stop", "STOPPED"}, {"start", "RUNNING"}, {"reset", "RUNNING"}, {"softreset", "RUNNING"},
	} {
		var acted pool

		resp := h.do(http.MethodPost, "/20160918/instancePools/"+created.ID+"/actions/"+step.action, nil)
		assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"), step.action)
		h.decode(resp, &acted)
		assert.Equal(t, step.want, acted.LifecycleState, step.action)
	}

	getAction := h.do(http.MethodGet, "/20160918/instancePools/"+created.ID+"/actions/start", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, getAction.StatusCode)

	postMembers := h.do(http.MethodPost, "/20160918/instancePools/"+created.ID+"/instances", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, postMembers.StatusCode)

	noCompartment := h.do(http.MethodGet, "/20160918/instancePools/"+created.ID+"/instances", nil)
	assert.Equal(t, http.StatusBadRequest, noCompartment.StatusCode)

	unknownCfg := h.do(http.MethodPost, "/20160918/instancePools", map[string]any{
		"compartmentId": compartment, "instanceConfigurationId": "ocid1.instanceconfiguration.oc1.iad.missing",
	})
	assert.Equal(t, http.StatusNotFound, unknownCfg.StatusCode)

	noPoolCompartment := h.do(http.MethodPost, "/20160918/instancePools", map[string]any{"instanceConfigurationId": cfg.ID})
	assert.Equal(t, http.StatusBadRequest, noPoolCompartment.StatusCode)

	term := h.do(http.MethodDelete, "/20160918/instancePools/"+created.ID, nil)
	assert.Equal(t, http.StatusNoContent, term.StatusCode)

	const missing = "/20160918/instancePools/ocid1.instancepool.oc1.iad.missing"

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/20160918/instancePools/" + created.ID},
		{http.MethodDelete, "/20160918/instancePools/" + created.ID},
		{http.MethodPut, missing},
		{http.MethodPost, missing + "/actions/start"},
		{http.MethodGet, missing + "/instances?compartmentId=" + compartment},
	} {
		resp := h.do(tc.method, tc.path, map[string]any{})
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, tc.method+" "+tc.path)
	}
}

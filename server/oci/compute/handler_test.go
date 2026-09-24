package compute_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	computeprovider "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	ocicompute "github.com/stackshy/cloudemu/v2/server/oci/compute"
	ocivcn "github.com/stackshy/cloudemu/v2/server/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const (
	compartment = "ocid1.compartment.oc1..test"
	shape       = "VM.Standard.E4.Flex"
)

type harness struct {
	t       *testing.T
	server  *httptest.Server
	compute *computeprovider.Mock
	vcn     *vcnprovider.Mock
	subnet  string
	vcnID   string
	image   string
}

// newHarness serves the compute handler behind the VCN one, in the same order
// server/oci.New registers them, so a test also proves the two do not collide.
func newHarness(t *testing.T) *harness {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartment))

	vcnMock := vcnprovider.New(opts)
	computeMock := computeprovider.New(opts)
	computeMock.SetNetworking(vcnMock)

	work := workrequest.New(opts)

	mux := http.NewServeMux()
	vcnHandler := ocivcn.New(vcnMock, work)
	computeHandler := ocicompute.New(computeMock, work)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case vcnHandler.Matches(r):
			vcnHandler.ServeHTTP(w, r)
		case computeHandler.Matches(r):
			computeHandler.ServeHTTP(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	h := &harness{t: t, server: httptest.NewServer(mux), compute: computeMock, vcn: vcnMock}
	t.Cleanup(h.server.Close)

	var vcn struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/vcns", map[string]any{
		"compartmentId": compartment, "cidrBlock": "10.0.0.0/16", "displayName": "app",
	}), &vcn)
	h.vcnID = vcn.ID

	var subnet struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/subnets", map[string]any{
		"compartmentId": compartment, "vcnId": vcn.ID, "cidrBlock": "10.0.1.0/24",
	}), &subnet)
	h.subnet = subnet.ID

	var images struct{ Items []struct{ ID string } }

	resp := h.do(http.MethodGet, "/20160918/images?compartmentId="+compartment, nil)
	h.decodeList(resp, &images.Items)
	require.NotEmpty(t, images.Items)
	h.image = images.Items[0].ID

	return h
}

func (h *harness) do(method, path string, body any) *http.Response {
	h.t.Helper()

	var buf bytes.Buffer

	if body != nil {
		require.NoError(h.t, json.NewEncoder(&buf).Encode(body))
	}

	req, err := http.NewRequestWithContext(h.t.Context(), method, h.server.URL+path, &buf)
	require.NoError(h.t, err)

	resp, err := h.server.Client().Do(req)
	require.NoError(h.t, err)

	h.t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

func (h *harness) decode(resp *http.Response, out any) {
	h.t.Helper()

	require.Less(h.t, resp.StatusCode, http.StatusBadRequest, "unexpected status %d", resp.StatusCode)
	require.NoError(h.t, json.NewDecoder(resp.Body).Decode(out))
}

func (h *harness) decodeList(resp *http.Response, out any) {
	h.t.Helper()
	h.decode(resp, out)
}

func TestMatchesClaimsOnlyComputeCollections(t *testing.T) {
	h := ocicompute.New(nil, nil)

	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{name: "instances", path: "/20160918/instances", expect: true},
		{name: "one instance", path: "/20160918/instances/ocid1.instance.oc1.iad.a", expect: true},
		{name: "shapes", path: "/20160918/shapes", expect: true},
		{name: "images", path: "/20160918/images", expect: true},
		{name: "volumes", path: "/20160918/volumes", expect: true},
		{name: "volume attachments", path: "/20160918/volumeAttachments", expect: true},
		{name: "boot volumes", path: "/20160918/bootVolumes", expect: true},
		{name: "boot volume attachments", path: "/20160918/bootVolumeAttachments", expect: true},
		{name: "volume backups", path: "/20160918/volumeBackups", expect: true},
		{name: "boot volume backups", path: "/20160918/bootVolumeBackups", expect: true},
		{name: "volume groups", path: "/20160918/volumeGroups", expect: true},
		{name: "vnic attachments", path: "/20160918/vnicAttachments", expect: true},
		{name: "instance configurations", path: "/20160918/instanceConfigurations", expect: true},
		{name: "instance pools", path: "/20160918/instancePools", expect: true},
		{name: "pool instances", path: "/20160918/instancePools/x/instances", expect: true},
		{name: "unemulated but claimed", path: "/20160918/dedicatedVmHosts", expect: true},

		// Everything server/oci/vcn owns must fall through to it.
		{name: "vcns is VCN's", path: "/20160918/vcns"},
		{name: "subnets is VCN's", path: "/20160918/subnets"},
		{name: "vnics is VCN's", path: "/20160918/vnics/ocid1.vnic.oc1.iad.a"},
		{name: "security lists are VCN's", path: "/20160918/securityLists"},
		{name: "NSGs are VCN's", path: "/20160918/networkSecurityGroups"},
		{name: "route tables are VCN's", path: "/20160918/routeTables"},
		{name: "public IPs are VCN's", path: "/20160918/publicIps"},

		// And the shared poller's, and other services' API versions.
		{name: "work requests are the poller's", path: "/20160918/workRequests"},
		{name: "another API version", path: "/20180222/instances"},
		{name: "too many segments", path: "/20160918/instances/a/b/c/d"},
		{name: "no collection", path: "/20160918"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			assert.Equal(t, tc.expect, h.Matches(req))
		})
	}
}

// TestVCNTrafficIsNotSwallowed proves the two handlers coexist: the VCN paths
// still answer with the VCN handler behind the same mux.
func TestVCNTrafficIsNotSwallowed(t *testing.T) {
	h := newHarness(t)

	resp := h.do(http.MethodGet, "/20160918/subnets?compartmentId="+compartment, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var subnets []struct {
		ID    string `json:"id"`
		VCNID string `json:"vcnId"`
	}

	h.decode(resp, &subnets)
	require.Len(t, subnets, 1)
	assert.Equal(t, h.vcnID, subnets[0].VCNID)
}

func TestDriverWithoutExtrasIsNotImplemented(t *testing.T) {
	h := ocicompute.New(bareDriver{}, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/20160918/instances?compartmentId=x", nil))

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	assert.Contains(t, rec.Body.String(), "does not implement OCI compartments")
}

func TestLaunchGetAttachAndTerminate(t *testing.T) {
	h := newHarness(t)

	resp := h.do(http.MethodPost, "/20160918/instances", map[string]any{
		"compartmentId":      compartment,
		"availabilityDomain": "cloudemu:US-ASHBURN-1-AD-1",
		"displayName":        "web-1",
		"shape":              shape,
		"imageId":            h.image,
		"shapeConfig":        map[string]any{"ocpus": 2, "memoryInGBs": 32},
		"metadata":           map[string]string{"ssh_authorized_keys": "ssh-rsa AAAA"},
		"createVnicDetails":  map[string]any{"subnetId": h.subnet, "hostnameLabel": "web1"},
		"freeformTags":       map[string]string{"env": "prod"},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"), "a launch records a work request")

	var inst struct {
		ID                 string            `json:"id"`
		CompartmentID      string            `json:"compartmentId"`
		DisplayName        string            `json:"displayName"`
		Shape              string            `json:"shape"`
		LifecycleState     string            `json:"lifecycleState"`
		Region             string            `json:"region"`
		Metadata           map[string]string `json:"metadata"`
		FreeformTags       map[string]string `json:"freeformTags"`
		AvailabilityDomain string            `json:"availabilityDomain"`
		ShapeConfig        struct {
			Ocpus float32 `json:"ocpus"`
		} `json:"shapeConfig"`
	}

	h.decode(resp, &inst)
	assert.Equal(t, compartment, inst.CompartmentID)
	assert.Equal(t, "web-1", inst.DisplayName)
	assert.Equal(t, "RUNNING", inst.LifecycleState)
	assert.Equal(t, "iad", inst.Region)
	assert.Equal(t, "ssh-rsa AAAA", inst.Metadata["ssh_authorized_keys"])
	assert.Equal(t, "prod", inst.FreeformTags["env"])
	assert.NotContains(t, inst.FreeformTags, "Name", "internal tags never reach freeformTags")
	assert.InDelta(t, 2.0, inst.ShapeConfig.Ocpus, 0.001)

	got := h.do(http.MethodGet, "/20160918/instances/"+inst.ID, nil)
	require.Equal(t, http.StatusOK, got.StatusCode)

	// The launch created a VNIC attachment holding the VCN service's VNIC.
	var attachments []struct {
		InstanceID string `json:"instanceId"`
		VnicID     string `json:"vnicId"`
		SubnetID   string `json:"subnetId"`
	}

	h.decode(h.do(http.MethodGet,
		"/20160918/vnicAttachments?compartmentId="+compartment+"&instanceId="+inst.ID, nil), &attachments)
	require.Len(t, attachments, 1)
	assert.Equal(t, h.subnet, attachments[0].SubnetID)
	assert.True(t, strings.HasPrefix(attachments[0].VnicID, "ocid1.vnic."), "got %q", attachments[0].VnicID)

	var vol struct {
		ID             string `json:"id"`
		LifecycleState string `json:"lifecycleState"`
		SizeInGBs      int    `json:"sizeInGBs"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumes", map[string]any{
		"compartmentId": compartment, "displayName": "data", "sizeInGBs": 100,
	}), &vol)
	assert.Equal(t, "AVAILABLE", vol.LifecycleState)
	assert.Equal(t, 100, vol.SizeInGBs)

	var att struct {
		ID             string `json:"id"`
		AttachmentType string `json:"attachmentType"`
		LifecycleState string `json:"lifecycleState"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumeAttachments", map[string]any{
		"instanceId": inst.ID, "volumeId": vol.ID, "device": "/dev/oracleoci/oraclevdb",
	}), &att)
	assert.Equal(t, "ATTACHED", att.LifecycleState)
	assert.Equal(t, "paravirtualized", att.AttachmentType)

	var listed []struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/instances?compartmentId="+compartment, nil), &listed)
	require.Len(t, listed, 1)
	assert.Equal(t, inst.ID, listed[0].ID)

	stopped := h.do(http.MethodPost, "/20160918/instances/"+inst.ID+"?action=STOP", nil)
	require.Equal(t, http.StatusOK, stopped.StatusCode)
	assert.NotEmpty(t, stopped.Header.Get("opc-work-request-id"))

	var after struct {
		LifecycleState string `json:"lifecycleState"`
	}

	h.decode(stopped, &after)
	assert.Equal(t, "STOPPED", after.LifecycleState)

	term := h.do(http.MethodDelete, "/20160918/instances/"+inst.ID, nil)
	assert.Equal(t, http.StatusNoContent, term.StatusCode)
	assert.NotEmpty(t, term.Header.Get("opc-work-request-id"))

	gone := h.do(http.MethodGet, "/20160918/instances/"+inst.ID, nil)
	assert.Equal(t, http.StatusNotFound, gone.StatusCode)
}

func (h *harness) launch(displayName string) string {
	h.t.Helper()

	var inst struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", map[string]any{
		"compartmentId":     compartment,
		"displayName":       displayName,
		"shape":             shape,
		"imageId":           h.image,
		"createVnicDetails": map[string]any{"subnetId": h.subnet},
	}), &inst)

	return inst.ID
}

func TestListsRequireACompartment(t *testing.T) {
	h := newHarness(t)

	for _, collection := range []string{
		"instances", "shapes", "images", "volumes", "volumeAttachments", "bootVolumes",
		"bootVolumeAttachments", "volumeBackups", "bootVolumeBackups", "volumeGroups",
		"vnicAttachments", "instanceConfigurations", "instancePools",
	} {
		t.Run(collection, func(t *testing.T) {
			resp := h.do(http.MethodGet, "/20160918/"+collection, nil)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Contains(t, bodyOf(t, resp), "compartmentId is required")
		})
	}
}

func TestListsFilterByCompartment(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	var mine []struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/instances?compartmentId="+compartment, nil), &mine)
	require.Len(t, mine, 1)
	assert.Equal(t, id, mine[0].ID)

	var theirs []struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodGet,
		"/20160918/instances?compartmentId=ocid1.compartment.oc1..other", nil), &theirs)
	assert.Empty(t, theirs, "an instance in another compartment does not list")
}

func TestChangeCompartmentMovesAnInstance(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	resp := h.do(http.MethodPost, "/20160918/instances/"+id+"/actions/changeCompartment", map[string]any{
		"compartmentId": "ocid1.compartment.oc1..other",
	})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("opc-work-request-id"))

	var moved []struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodGet,
		"/20160918/instances?compartmentId=ocid1.compartment.oc1..other", nil), &moved)
	require.Len(t, moved, 1)
	assert.Equal(t, id, moved[0].ID)
}

func TestErrorPerOperationFamily(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		detail string
	}{
		{
			name: "launch without a compartment", method: http.MethodPost, path: "/20160918/instances",
			body:   map[string]any{"shape": shape, "imageId": "x"},
			status: http.StatusBadRequest, detail: "compartmentId is required",
		},
		{
			name: "launch with an unknown shape", method: http.MethodPost, path: "/20160918/instances",
			body:   map[string]any{"compartmentId": compartment, "shape": "VM.Nope.1", "imageId": "x"},
			status: http.StatusNotFound, detail: "shape VM.Nope.1 not found",
		},
		{
			name: "unknown instance", method: http.MethodGet,
			path:   "/20160918/instances/ocid1.instance.oc1.iad.missing",
			status: http.StatusNotFound, detail: "NotAuthorizedOrNotFound",
		},
		{
			name: "unemulated instance action", method: http.MethodPost,
			path:   "/20160918/instances/ocid1.instance.oc1.iad.a?action=REBOOTMIGRATE",
			status: http.StatusNotImplemented, detail: "is not emulated",
		},
		{
			name: "unknown volume", method: http.MethodGet, path: "/20160918/volumes/ocid1.volume.oc1.iad.x",
			status: http.StatusNotFound, detail: "NotAuthorizedOrNotFound",
		},
		{
			name: "attach without a volume", method: http.MethodPost, path: "/20160918/volumeAttachments",
			body:   map[string]any{"instanceId": "ocid1.instance.oc1.iad.a"},
			status: http.StatusBadRequest, detail: "instanceId and volumeId are required",
		},
		{
			name: "boot volume without a source", method: http.MethodPost, path: "/20160918/bootVolumes",
			body:   map[string]any{"compartmentId": compartment},
			status: http.StatusBadRequest, detail: "sourceDetails is required",
		},
		{
			name: "backup without a volume", method: http.MethodPost, path: "/20160918/volumeBackups",
			body:   map[string]any{"displayName": "x"},
			status: http.StatusBadRequest, detail: "volumeId is required",
		},
		{
			name: "volume group with an unmodelled source", method: http.MethodPost, path: "/20160918/volumeGroups",
			body: map[string]any{
				"compartmentId": compartment,
				"sourceDetails": map[string]any{"type": "volumeGroupBackupId"},
			},
			status: http.StatusNotImplemented, detail: "is not emulated",
		},
		{
			name: "instance configuration without launch details", method: http.MethodPost,
			path:   "/20160918/instanceConfigurations",
			body:   map[string]any{"compartmentId": compartment},
			status: http.StatusBadRequest, detail: "instanceDetails.launchDetails is required",
		},
		{
			name: "pool without a configuration", method: http.MethodPost, path: "/20160918/instancePools",
			body:   map[string]any{"compartmentId": compartment},
			status: http.StatusBadRequest, detail: "instanceConfigurationId is required",
		},
		{
			name: "unemulated collection", method: http.MethodGet,
			path:   "/20160918/computeCapacityReservations?compartmentId=" + compartment,
			status: http.StatusNotImplemented, detail: "is not emulated",
		},
		{
			name: "shapes have no member", method: http.MethodGet, path: "/20160918/shapes/VM.Standard2.1",
			status: http.StatusNotFound, detail: "no member resource",
		},
		{
			name: "unsupported verb", method: http.MethodPatch, path: "/20160918/volumes",
			status: http.StatusMethodNotAllowed, detail: "method not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.do(tc.method, tc.path, tc.body)
			assert.Equal(t, tc.status, resp.StatusCode)
			assert.Contains(t, bodyOf(t, resp), tc.detail)
		})
	}
}

func TestImageCaptureAndShapes(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	var img struct {
		ID              string `json:"id"`
		DisplayName     string `json:"displayName"`
		BaseImageID     string `json:"baseImageId"`
		OperatingSystem string `json:"operatingSystem"`
		LifecycleState  string `json:"lifecycleState"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/images", map[string]any{
		"compartmentId": compartment, "instanceId": id, "displayName": "golden",
	}), &img)
	assert.Equal(t, "golden", img.DisplayName)
	assert.Equal(t, h.image, img.BaseImageID)
	assert.Equal(t, "AVAILABLE", img.LifecycleState)

	var shapes []struct {
		Shape       string `json:"shape"`
		IsFlexible  bool   `json:"isFlexible"`
		OcpuOptions *struct {
			Max float32 `json:"max"`
		} `json:"ocpuOptions"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/shapes?compartmentId="+compartment, nil), &shapes)
	require.NotEmpty(t, shapes)

	for i := range shapes {
		if shapes[i].Shape == shape {
			assert.True(t, shapes[i].IsFlexible)
			require.NotNil(t, shapes[i].OcpuOptions)
			assert.Positive(t, shapes[i].OcpuOptions.Max)

			return
		}
	}

	t.Fatalf("shape %s is not in the catalogue", shape)
}

func TestBackupsAreSplitAcrossTheirCollections(t *testing.T) {
	h := newHarness(t)
	id := h.launch("web-1")

	details, ok := h.compute.InstanceDetails(id)
	require.True(t, ok)

	var vol struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumes", map[string]any{
		"compartmentId": compartment, "sizeInGBs": 50,
	}), &vol)

	var blockBackup struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		VolumeID string `json:"volumeId"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/volumeBackups", map[string]any{
		"volumeId": vol.ID, "displayName": "nightly", "type": "FULL",
	}), &blockBackup)
	assert.Equal(t, "FULL", blockBackup.Type)
	assert.Equal(t, vol.ID, blockBackup.VolumeID)

	var bootBackup struct {
		ID           string `json:"id"`
		BootVolumeID string `json:"bootVolumeId"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/bootVolumeBackups", map[string]any{
		"bootVolumeId": details.BootVolumeID, "displayName": "boot",
	}), &bootBackup)
	assert.Equal(t, details.BootVolumeID, bootBackup.BootVolumeID)

	var blockList []struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodGet, "/20160918/volumeBackups?compartmentId="+compartment, nil), &blockList)
	require.Len(t, blockList, 1)
	assert.Equal(t, blockBackup.ID, blockList[0].ID)

	// Reaching for a boot backup through the block collection is a not-found.
	resp := h.do(http.MethodGet, "/20160918/volumeBackups/"+bootBackup.ID, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestInstanceConfigurationAndPool(t *testing.T) {
	h := newHarness(t)

	var cfg struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instanceConfigurations", map[string]any{
		"compartmentId": compartment,
		"displayName":   "web-tpl",
		"instanceDetails": map[string]any{
			"instanceType": "compute",
			"launchDetails": map[string]any{
				"shape":             shape,
				"sourceDetails":     map[string]any{"sourceType": "image", "imageId": h.image},
				"createVnicDetails": map[string]any{"subnetId": h.subnet},
			},
		},
	}), &cfg)
	require.NotEmpty(t, cfg.ID)

	var launched struct {
		ID    string `json:"id"`
		Shape string `json:"shape"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instanceConfigurations/"+cfg.ID+"/actions/launch", nil), &launched)
	assert.Equal(t, shape, launched.Shape)

	var pool struct {
		ID             string `json:"id"`
		Size           int    `json:"size"`
		LifecycleState string `json:"lifecycleState"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instancePools", map[string]any{
		"compartmentId": compartment, "displayName": "web", "instanceConfigurationId": cfg.ID, "size": 2,
	}), &pool)
	assert.Equal(t, 2, pool.Size)
	assert.Equal(t, "RUNNING", pool.LifecycleState)

	var members []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}

	h.decode(h.do(http.MethodGet,
		"/20160918/instancePools/"+pool.ID+"/instances?compartmentId="+compartment, nil), &members)
	require.Len(t, members, 2)
	assert.Equal(t, "RUNNING", members[0].State)

	var stopped struct {
		LifecycleState string `json:"lifecycleState"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instancePools/"+pool.ID+"/actions/stop", nil), &stopped)
	assert.Equal(t, "STOPPED", stopped.LifecycleState)

	bad := h.do(http.MethodPost, "/20160918/instancePools/"+pool.ID+"/actions/detach", nil)
	assert.Equal(t, http.StatusNotImplemented, bad.StatusCode)

	term := h.do(http.MethodDelete, "/20160918/instancePools/"+pool.ID, nil)
	assert.Equal(t, http.StatusNoContent, term.StatusCode)
}

// TestSubnetSecurityListsReachTheInstance pins the coupling the connectivity
// fix rests on: a subnet created through the VCN handler carries the security
// lists governing it, and a launch records them on the instance.
func TestSubnetSecurityListsReachTheInstance(t *testing.T) {
	h := newHarness(t)

	var list struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/securityLists", map[string]any{
		"compartmentId": compartment, "vcnId": h.vcnID, "displayName": "web",
		"ingressSecurityRules": []map[string]any{
			{"protocol": "6", "source": "10.0.0.0/16", "tcpOptions": map[string]any{
				"destinationPortRange": map[string]int{"min": 443, "max": 443},
			}},
		},
	}), &list)

	var subnet struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/subnets", map[string]any{
		"compartmentId": compartment, "vcnId": h.vcnID, "cidrBlock": "10.0.9.0/24",
		"securityListIds": []string{list.ID},
	}), &subnet)

	var inst struct {
		ID string `json:"id"`
	}

	h.decode(h.do(http.MethodPost, "/20160918/instances", map[string]any{
		"compartmentId":     compartment,
		"shape":             shape,
		"imageId":           h.image,
		"createVnicDetails": map[string]any{"subnetId": subnet.ID},
	}), &inst)

	got, err := h.compute.DescribeInstances(t.Context(), []string{inst.ID}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].SecurityGroups, list.ID,
		"the subnet's security list governs the instance, so connectivity analysis sees it")
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()

	var buf bytes.Buffer

	_, err := buf.ReadFrom(resp.Body)
	require.NoError(t, err)

	return buf.String()
}

// bareDriver is a compute driver that implements nothing beyond the portable
// interface, so the handler has to report the OCI-only surface as missing.
type bareDriver struct {
	computedriver.Compute
}

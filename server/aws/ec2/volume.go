package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Default volume type AWS uses when the caller omits VolumeType.
const defaultVolumeType = "gp3"

// Storage describe filter names shared across volume/snapshot/image handlers.
// filterTagKey and filterState are defined in networking_common.go (shared).
const (
	filterAvailabilityZone   = "availability-zone"
	filterSize               = "size"
	filterEncrypted          = "encrypted"
	filterArchitecture       = "architecture"
	filterHypervisor         = "hypervisor"
	filterVolumeID           = "volume-id"
	filterStatus             = "status"
	filterVolumeType         = "volume-type"
	filterSnapshotID         = "snapshot-id"
	filterOwnerID            = "owner-id"
	filterDescription        = "description"
	filterImageID            = "image-id"
	filterImageType          = "image-type"
	filterName               = "name"
	filterRootDeviceType     = "root-device-type"
	filterVirtualizationType = "virtualization-type"
)

type volumeAttachmentXML struct {
	VolumeID   string `xml:"volumeId"`
	InstanceID string `xml:"instanceId"`
	Device     string `xml:"device"`
	Status     string `xml:"status"`
	AttachTime string `xml:"attachTime,omitempty"`
}

type volumeXML struct {
	VolumeID         string                `xml:"volumeId"`
	Size             int                   `xml:"size"`
	SnapshotID       string                `xml:"snapshotId"`
	Status           string                `xml:"status"`
	VolumeType       string                `xml:"volumeType"`
	AvailabilityZone string                `xml:"availabilityZone"`
	CreateTime       string                `xml:"createTime,omitempty"`
	Iops             int                   `xml:"iops,omitempty"`
	Throughput       int                   `xml:"throughput,omitempty"`
	Encrypted        bool                  `xml:"encrypted"`
	KmsKeyID         string                `xml:"kmsKeyId,omitempty"`
	Attachments      []volumeAttachmentXML `xml:"attachmentSet>item,omitempty"`
	Tags             []tagItem             `xml:"tagSet>item,omitempty"`
}

// createVolumeResponseXML inlines volume fields directly under the response
// root (AWS CreateVolume has no <volume> wrapper, unlike CreateVpc).
type createVolumeResponseXML struct {
	XMLName   xml.Name `xml:"CreateVolumeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	volumeXML
}

type describeVolumesResponseXML struct {
	XMLName   xml.Name    `xml:"DescribeVolumesResponse"`
	Xmlns     string      `xml:"xmlns,attr"`
	RequestID string      `xml:"requestId"`
	VolumeSet []volumeXML `xml:"volumeSet>item"`
}

type deleteVolumeResponseXML struct {
	XMLName   xml.Name `xml:"DeleteVolumeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type attachVolumeResponseXML struct {
	XMLName    xml.Name `xml:"AttachVolumeResponse"`
	Xmlns      string   `xml:"xmlns,attr"`
	RequestID  string   `xml:"requestId"`
	VolumeID   string   `xml:"volumeId"`
	InstanceID string   `xml:"instanceId"`
	Device     string   `xml:"device"`
	Status     string   `xml:"status"`
	AttachTime string   `xml:"attachTime,omitempty"`
}

type detachVolumeResponseXML struct {
	XMLName    xml.Name `xml:"DetachVolumeResponse"`
	Xmlns      string   `xml:"xmlns,attr"`
	RequestID  string   `xml:"requestId"`
	VolumeID   string   `xml:"volumeId"`
	InstanceID string   `xml:"instanceId,omitempty"`
	Device     string   `xml:"device,omitempty"`
	Status     string   `xml:"status"`
}

func (h *Handler) createVolume(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.Form.Get("Size"))
	vt := r.Form.Get("VolumeType")

	if vt == "" {
		vt = defaultVolumeType
	}

	iops, _ := strconv.Atoi(r.Form.Get("Iops"))
	throughput, _ := strconv.Atoi(r.Form.Get("Throughput"))

	cfg := computedriver.VolumeConfig{
		Size:             size,
		VolumeType:       vt,
		AvailabilityZone: r.Form.Get("AvailabilityZone"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "volume"),
		IOPS:             iops,
		Throughput:       throughput,
		Encrypted:        r.Form.Get("Encrypted") == formTrue,
		SnapshotID:       r.Form.Get("SnapshotId"),
		KmsKeyID:         r.Form.Get("KmsKeyId"),
	}

	info, err := h.compute.CreateVolume(r.Context(), cfg)
	if err != nil {
		writeVolumeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createVolumeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		volumeXML: toVolumeXML(info),
	})
}

func (h *Handler) deleteVolume(w http.ResponseWriter, r *http.Request) {
	if err := h.compute.DeleteVolume(r.Context(), r.Form.Get("VolumeId")); err != nil {
		writeVolumeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteVolumeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe+filter pattern; siblings in snapshot/image
func (h *Handler) describeVolumes(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VolumeId")
	filters := awsquery.Filters(r.Form)

	if err := validateVolumeFilters(filters); err != nil {
		writeVolumeErr(w, err)
		return
	}

	vols, err := h.compute.DescribeVolumes(r.Context(), ids)
	if err != nil {
		writeVolumeErr(w, err)
		return
	}

	out := make([]volumeXML, 0, len(vols))

	for i := range vols {
		if volumeMatchesFilters(&vols[i], filters) {
			out = append(out, toVolumeXML(&vols[i]))
		}
	}

	awsquery.WriteXMLResponse(w, describeVolumesResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VolumeSet: out,
	})
}

func validateVolumeFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		if isStorageTagFilter(f.Name) {
			continue
		}

		switch f.Name {
		case filterVolumeID, filterStatus, filterAvailabilityZone, filterVolumeType,
			filterEncrypted, filterSnapshotID, filterSize:
		default:
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

func volumeMatchesFilters(v *computedriver.VolumeInfo, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !volumeMatchesFilter(v, f) {
			return false
		}
	}

	return true
}

func volumeMatchesFilter(v *computedriver.VolumeInfo, f awsquery.Filter) bool {
	if matched, isTag := matchStorageTagFilter(v.Tags, f); isTag {
		return matched
	}

	switch f.Name {
	case filterVolumeID:
		return containsString(f.Values, v.ID)
	case filterStatus:
		return containsString(f.Values, nonEmpty(v.State, stateAvailable))
	case filterAvailabilityZone:
		return containsString(f.Values, v.AvailabilityZone)
	case filterVolumeType:
		return containsString(f.Values, v.VolumeType)
	case filterEncrypted:
		return containsString(f.Values, boolFilterValue(v.Encrypted))
	case filterSnapshotID:
		return containsString(f.Values, v.SnapshotID)
	case filterSize:
		return containsString(f.Values, strconv.Itoa(v.Size))
	default:
		return false
	}
}

func (h *Handler) attachVolume(w http.ResponseWriter, r *http.Request) {
	volID := r.Form.Get("VolumeId")
	instID := r.Form.Get("InstanceId")
	device := r.Form.Get("Device")

	if err := h.compute.AttachVolume(r.Context(), volID, instID, device); err != nil {
		writeVolumeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, attachVolumeResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		VolumeID:   volID,
		InstanceID: instID,
		Device:     device,
		Status:     "attaching",
	})
}

func (h *Handler) detachVolume(w http.ResponseWriter, r *http.Request) {
	volID := r.Form.Get("VolumeId")

	if err := h.compute.DetachVolume(r.Context(), volID); err != nil {
		writeVolumeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, detachVolumeResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		VolumeID:   volID,
		InstanceID: r.Form.Get("InstanceId"),
		Device:     r.Form.Get("Device"),
		Status:     "detaching",
	})
}

// volumeModificationXML mirrors the AWS EC2 VolumeModification structure
// returned by ModifyVolume.
type volumeModificationXML struct {
	VolumeID           string `xml:"volumeId"`
	ModificationState  string `xml:"modificationState"`
	StartTime          string `xml:"startTime,omitempty"`
	Progress           int    `xml:"progress"`
	OriginalSize       int    `xml:"originalSize"`
	OriginalIops       int    `xml:"originalIops,omitempty"`
	OriginalThroughput int    `xml:"originalThroughput,omitempty"`
	OriginalVolumeType string `xml:"originalVolumeType"`
	TargetSize         int    `xml:"targetSize"`
	TargetIops         int    `xml:"targetIops,omitempty"`
	TargetThroughput   int    `xml:"targetThroughput,omitempty"`
	TargetVolumeType   string `xml:"targetVolumeType"`
}

type modifyVolumeResponseXML struct {
	XMLName            xml.Name              `xml:"ModifyVolumeResponse"`
	Xmlns              string                `xml:"xmlns,attr"`
	RequestID          string                `xml:"requestId"`
	VolumeModification volumeModificationXML `xml:"volumeModification"`
}

// modifyVolume handles Action=ModifyVolume. It is served by the AWS-only
// VolumeModifier capability; a compute driver that does not implement it
// reports Unimplemented.
func (h *Handler) modifyVolume(w http.ResponseWriter, r *http.Request) {
	modifier, ok := h.compute.(computedriver.VolumeModifier)
	if !ok {
		writeVolumeErr(w, cerrors.New(cerrors.Unimplemented, "ModifyVolume is not supported"))
		return
	}

	size, _ := strconv.Atoi(r.Form.Get("Size"))
	iops, _ := strconv.Atoi(r.Form.Get("Iops"))
	throughput, _ := strconv.Atoi(r.Form.Get("Throughput"))

	mod, err := modifier.ModifyVolume(r.Context(), computedriver.ModifyVolumeInput{
		VolumeID:   r.Form.Get("VolumeId"),
		Size:       size,
		IOPS:       iops,
		Throughput: throughput,
		VolumeType: r.Form.Get("VolumeType"),
	})
	if err != nil {
		writeVolumeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyVolumeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VolumeModification: volumeModificationXML{
			VolumeID:           mod.VolumeID,
			ModificationState:  mod.ModificationState,
			StartTime:          mod.StartTime,
			Progress:           mod.Progress,
			OriginalSize:       mod.OriginalSize,
			OriginalIops:       mod.OriginalIOPS,
			OriginalThroughput: mod.OriginalThroughput,
			OriginalVolumeType: mod.OriginalVolumeType,
			TargetSize:         mod.TargetSize,
			TargetIops:         mod.TargetIOPS,
			TargetThroughput:   mod.TargetThroughput,
			TargetVolumeType:   mod.TargetVolumeType,
		},
	})
}

func toVolumeXML(v *computedriver.VolumeInfo) volumeXML {
	state := v.State
	if state == "" {
		state = stateAvailable
	}

	x := volumeXML{
		VolumeID:         v.ID,
		Size:             v.Size,
		SnapshotID:       v.SnapshotID,
		Status:           state,
		VolumeType:       v.VolumeType,
		AvailabilityZone: v.AvailabilityZone,
		CreateTime:       v.CreatedAt,
		Iops:             v.IOPS,
		Throughput:       v.Throughput,
		Encrypted:        v.Encrypted,
		KmsKeyID:         v.KmsKeyID,
		Tags:             toTagItems(v.Tags),
	}

	if v.AttachedTo != "" {
		x.Attachments = []volumeAttachmentXML{{
			VolumeID:   v.ID,
			InstanceID: v.AttachedTo,
			Device:     v.Device,
			Status:     "attached",
			AttachTime: v.CreatedAt,
		}}
	}

	return x
}

func writeVolumeErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVolume.NotFound", "IncorrectState")
}

// isStorageTagFilter reports whether name is a tag-based filter (tag:<key> or
// tag-key) shared by volume/snapshot/image describes.
func isStorageTagFilter(name string) bool {
	return name == filterTagKey || strings.HasPrefix(name, "tag:")
}

// matchStorageTagFilter evaluates a tag:<key> or tag-key filter against a
// resource's tags. The second return reports whether the filter was a tag
// filter at all, so callers can fall through to resource-specific fields.
func matchStorageTagFilter(tags map[string]string, f awsquery.Filter) (matched, isTag bool) {
	switch {
	case strings.HasPrefix(f.Name, "tag:"):
		key := strings.TrimPrefix(f.Name, "tag:")
		v, ok := tags[key]

		return ok && containsString(f.Values, v), true
	case f.Name == filterTagKey:
		for k := range tags {
			if containsString(f.Values, k) {
				return true, true
			}
		}

		return false, true
	default:
		return false, false
	}
}

type volumeStatusItemXML struct {
	VolumeID         string `xml:"volumeId"`
	AvailabilityZone string `xml:"availabilityZone"`
	Status           string `xml:"volumeStatus>status"`
}

type describeVolumeStatusResponseXML struct {
	XMLName         xml.Name              `xml:"DescribeVolumeStatusResponse"`
	Xmlns           string                `xml:"xmlns,attr"`
	RequestID       string                `xml:"requestId"`
	VolumeStatusSet []volumeStatusItemXML `xml:"volumeStatusSet>item"`
}

// describeVolumeStatus reports per-volume health. The emulator has no failure
// modes, so every existing volume reports "ok".
func (h *Handler) describeVolumeStatus(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VolumeId")

	vols, err := h.compute.DescribeVolumes(r.Context(), ids)
	if err != nil {
		writeVolumeErr(w, err)
		return
	}

	out := make([]volumeStatusItemXML, 0, len(vols))
	for i := range vols {
		out = append(out, volumeStatusItemXML{
			VolumeID:         vols[i].ID,
			AvailabilityZone: vols[i].AvailabilityZone,
			Status:           "ok",
		})
	}

	awsquery.WriteXMLResponse(w, describeVolumeStatusResponseXML{
		Xmlns:           awsquery.Namespace,
		RequestID:       awsquery.RequestID,
		VolumeStatusSet: out,
	})
}

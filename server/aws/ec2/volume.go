package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// Default volume type AWS uses when the caller omits VolumeType.
const defaultVolumeType = "gp3"

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
	Status           string                `xml:"status"`
	VolumeType       string                `xml:"volumeType"`
	AvailabilityZone string                `xml:"availabilityZone"`
	CreateTime       string                `xml:"createTime,omitempty"`
	Encrypted        bool                  `xml:"encrypted"`
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

	cfg := computedriver.VolumeConfig{
		Size:             size,
		VolumeType:       vt,
		AvailabilityZone: r.Form.Get("AvailabilityZone"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "volume"),
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

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/sg/igw/route_table
func (h *Handler) describeVolumes(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "VolumeId")

	vols, err := h.compute.DescribeVolumes(r.Context(), ids)
	if err != nil {
		writeVolumeErr(w, err)
		return
	}

	out := make([]volumeXML, 0, len(vols))
	for i := range vols {
		out = append(out, toVolumeXML(&vols[i]))
	}

	awsquery.WriteXMLResponse(w, describeVolumesResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		VolumeSet: out,
	})
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

func toVolumeXML(v *computedriver.VolumeInfo) volumeXML {
	state := v.State
	if state == "" {
		state = stateAvailable
	}

	x := volumeXML{
		VolumeID:         v.ID,
		Size:             v.Size,
		Status:           state,
		VolumeType:       v.VolumeType,
		AvailabilityZone: v.AvailabilityZone,
		CreateTime:       v.CreatedAt,
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

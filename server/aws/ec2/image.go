package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

type imageBlockDeviceMappingXML struct {
	DeviceName          string `xml:"deviceName"`
	SnapshotID          string `xml:"ebs>snapshotId"`
	VolumeSize          int    `xml:"ebs>volumeSize"`
	VolumeType          string `xml:"ebs>volumeType"`
	DeleteOnTermination bool   `xml:"ebs>deleteOnTermination"`
}

type imageXML struct {
	ImageID             string                       `xml:"imageId"`
	State               string                       `xml:"imageState"`
	OwnerID             string                       `xml:"imageOwnerId,omitempty"`
	Name                string                       `xml:"name,omitempty"`
	Description         string                       `xml:"description,omitempty"`
	CreationDate        string                       `xml:"creationDate,omitempty"`
	Architecture        string                       `xml:"architecture"`
	ImageType           string                       `xml:"imageType,omitempty"`
	Public              bool                         `xml:"isPublic"`
	RootDeviceType      string                       `xml:"rootDeviceType,omitempty"`
	RootDeviceName      string                       `xml:"rootDeviceName,omitempty"`
	VirtualizationType  string                       `xml:"virtualizationType,omitempty"`
	Hypervisor          string                       `xml:"hypervisor,omitempty"`
	PlatformDetails     string                       `xml:"platformDetails,omitempty"`
	BlockDeviceMappings []imageBlockDeviceMappingXML `xml:"blockDeviceMapping>item,omitempty"`
	Tags                []tagItem                    `xml:"tagSet>item,omitempty"`
}

type createImageResponseXML struct {
	XMLName   xml.Name `xml:"CreateImageResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	ImageID   string   `xml:"imageId"`
}

type describeImagesResponseXML struct {
	XMLName   xml.Name   `xml:"DescribeImagesResponse"`
	Xmlns     string     `xml:"xmlns,attr"`
	RequestID string     `xml:"requestId"`
	ImagesSet []imageXML `xml:"imagesSet>item"`
}

type deregisterImageResponseXML struct {
	XMLName   xml.Name `xml:"DeregisterImageResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createImage(w http.ResponseWriter, r *http.Request) {
	cfg := computedriver.ImageConfig{
		InstanceID:  r.Form.Get("InstanceId"),
		Name:        r.Form.Get("Name"),
		Description: r.Form.Get("Description"),
		Tags:        mergeTagSpecs(awsquery.TagSpecs(r.Form), "image"),
	}

	info, err := h.compute.CreateImage(r.Context(), cfg)
	if err != nil {
		writeImageErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createImageResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		ImageID:   info.ID,
	})
}

type registerImageResponseXML struct {
	XMLName   xml.Name `xml:"RegisterImageResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	ImageID   string   `xml:"imageId"`
}

// registerImage handles Action=RegisterImage. It is served by the AWS-only
// ImageRegistrar capability; a compute driver that does not implement it
// reports Unimplemented.
func (h *Handler) registerImage(w http.ResponseWriter, r *http.Request) {
	registrar, ok := h.compute.(computedriver.ImageRegistrar)
	if !ok {
		writeImageErr(w, cerrors.New(cerrors.Unimplemented, "RegisterImage is not supported"))
		return
	}

	info, err := registrar.RegisterImage(r.Context(), computedriver.RegisterImageInput{
		Name:                r.Form.Get("Name"),
		Description:         r.Form.Get("Description"),
		Architecture:        r.Form.Get("Architecture"),
		RootDeviceName:      r.Form.Get("RootDeviceName"),
		VirtualizationType:  r.Form.Get("VirtualizationType"),
		BlockDeviceMappings: parseImageBlockDeviceMappings(r),
		Tags:                mergeTagSpecs(awsquery.TagSpecs(r.Form), "image"),
	})
	if err != nil {
		writeRegisterImageErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, registerImageResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		ImageID:   info.ID,
	})
}

// parseImageBlockDeviceMappings reads the BlockDeviceMapping.N.* query groups
// RegisterImage carries (DeviceName + nested Ebs.* fields).
func parseImageBlockDeviceMappings(r *http.Request) []computedriver.ImageBlockDeviceMapping {
	indices := awsquery.CollectIndices(r.Form, "BlockDeviceMapping")
	if len(indices) == 0 {
		return nil
	}

	out := make([]computedriver.ImageBlockDeviceMapping, 0, len(indices))

	for _, i := range indices {
		base := "BlockDeviceMapping." + strconv.Itoa(i)
		size, _ := strconv.Atoi(r.Form.Get(base + ".Ebs.VolumeSize"))

		out = append(out, computedriver.ImageBlockDeviceMapping{
			DeviceName:          r.Form.Get(base + ".DeviceName"),
			SnapshotID:          r.Form.Get(base + ".Ebs.SnapshotId"),
			VolumeSize:          size,
			VolumeType:          r.Form.Get(base + ".Ebs.VolumeType"),
			DeleteOnTermination: r.Form.Get(base+".Ebs.DeleteOnTermination") == formTrue,
		})
	}

	return out
}

// writeRegisterImageErr maps RegisterImage failures to their AWS codes: a
// duplicate Name is InvalidAMIName.Duplicate, and a missing referenced snapshot
// is InvalidSnapshot.NotFound (RegisterImage never reports InvalidAMIID).
func writeRegisterImageErr(w http.ResponseWriter, err error) {
	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAMIName.Duplicate", err.Error())
		return
	}

	writeErrWithNotFound(w, err, "InvalidSnapshot.NotFound", "IncorrectState")
}

func (h *Handler) deregisterImage(w http.ResponseWriter, r *http.Request) {
	if err := h.compute.DeregisterImage(r.Context(), r.Form.Get("ImageId")); err != nil {
		writeImageErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deregisterImageResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe+filter pattern; siblings in volume/snapshot
func (h *Handler) describeImages(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ImageId")
	filters := awsquery.Filters(r.Form)

	if err := validateImageFilters(filters); err != nil {
		writeImageErr(w, err)
		return
	}

	imgs, err := h.compute.DescribeImages(r.Context(), ids)
	if err != nil {
		writeImageErr(w, err)
		return
	}

	out := make([]imageXML, 0, len(imgs))

	for i := range imgs {
		if imageMatchesFilters(&imgs[i], filters) {
			out = append(out, toImageXML(&imgs[i]))
		}
	}

	awsquery.WriteXMLResponse(w, describeImagesResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		ImagesSet: out,
	})
}

func validateImageFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		if isStorageTagFilter(f.Name) {
			continue
		}

		switch f.Name {
		case filterImageID, filterName, filterState, filterOwnerID, filterArchitecture,
			filterRootDeviceType, filterVirtualizationType, filterHypervisor, filterImageType:
		default:
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

func imageMatchesFilters(img *computedriver.ImageInfo, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !imageMatchesFilter(img, f) {
			return false
		}
	}

	return true
}

//nolint:gocyclo // flat field dispatch; one case per supported DescribeImages filter
func imageMatchesFilter(img *computedriver.ImageInfo, f awsquery.Filter) bool {
	if matched, isTag := matchStorageTagFilter(img.Tags, f); isTag {
		return matched
	}

	switch f.Name {
	case filterImageID:
		return containsString(f.Values, img.ID)
	case filterName:
		return containsString(f.Values, img.Name)
	case filterState:
		return containsString(f.Values, nonEmpty(img.State, stateAvailable))
	case filterOwnerID:
		return containsString(f.Values, img.OwnerID)
	case filterArchitecture:
		return containsString(f.Values, nonEmpty(img.Architecture, "x86_64"))
	case filterRootDeviceType:
		return containsString(f.Values, img.RootDeviceType)
	case filterVirtualizationType:
		return containsString(f.Values, img.VirtualizationType)
	case filterHypervisor:
		return containsString(f.Values, img.Hypervisor)
	case filterImageType:
		return containsString(f.Values, img.ImageType)
	default:
		return false
	}
}

type describeImageAttributeResponseXML struct {
	XMLName   xml.Name  `xml:"DescribeImageAttributeResponse"`
	Xmlns     string    `xml:"xmlns,attr"`
	RequestID string    `xml:"requestId"`
	ImageID   string    `xml:"imageId"`
	Desc      *valueXML `xml:"description,omitempty"`
	BootMode  *valueXML `xml:"bootMode,omitempty"`
}

type valueXML struct {
	Value string `xml:"value"`
}

// describeImageAttribute returns a single AMI attribute. Only the attributes
// the emulator models (description, bootMode) carry a value; others return the
// image id with an empty attribute, matching the SDK's single-attribute shape.
func (h *Handler) describeImageAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("ImageId")

	imgs, err := h.compute.DescribeImages(r.Context(), []string{id})
	if err != nil {
		writeImageErr(w, err)
		return
	}

	resp := describeImageAttributeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		ImageID:   id,
	}

	switch r.Form.Get("Attribute") {
	case filterDescription:
		resp.Desc = &valueXML{Value: imgs[0].Description}
	case "bootMode":
		resp.BootMode = &valueXML{Value: ""}
	}

	awsquery.WriteXMLResponse(w, resp)
}

func toImageXML(img *computedriver.ImageInfo) imageXML {
	state := img.State
	if state == "" {
		state = stateAvailable
	}

	arch := img.Architecture
	if arch == "" {
		arch = "x86_64"
	}

	bdms := make([]imageBlockDeviceMappingXML, 0, len(img.BlockDeviceMappings))
	for _, b := range img.BlockDeviceMappings {
		bdms = append(bdms, imageBlockDeviceMappingXML{
			DeviceName:          b.DeviceName,
			SnapshotID:          b.SnapshotID,
			VolumeSize:          b.VolumeSize,
			VolumeType:          b.VolumeType,
			DeleteOnTermination: b.DeleteOnTermination,
		})
	}

	return imageXML{
		ImageID:             img.ID,
		State:               state,
		OwnerID:             img.OwnerID,
		Name:                img.Name,
		Description:         img.Description,
		CreationDate:        img.CreatedAt,
		Architecture:        arch,
		ImageType:           img.ImageType,
		Public:              false,
		RootDeviceType:      img.RootDeviceType,
		RootDeviceName:      img.RootDeviceName,
		VirtualizationType:  img.VirtualizationType,
		Hypervisor:          img.Hypervisor,
		PlatformDetails:     img.PlatformDetails,
		BlockDeviceMappings: bdms,
		Tags:                toTagItems(img.Tags),
	}
}

func writeImageErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidAMIID.NotFound", "IncorrectState")
}

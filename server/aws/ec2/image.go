package ec2

import (
	"encoding/xml"
	"net/http"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type imageXML struct {
	ImageID      string    `xml:"imageId"`
	State        string    `xml:"imageState"`
	Name         string    `xml:"name,omitempty"`
	Description  string    `xml:"description,omitempty"`
	CreationDate string    `xml:"creationDate,omitempty"`
	Architecture string    `xml:"architecture"`
	Public       bool      `xml:"isPublic"`
	Tags         []tagItem `xml:"tagSet>item,omitempty"`
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

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/sg/igw/route_table/volume/keypair/snapshot
func (h *Handler) describeImages(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ImageId")

	imgs, err := h.compute.DescribeImages(r.Context(), ids)
	if err != nil {
		writeImageErr(w, err)
		return
	}

	out := make([]imageXML, 0, len(imgs))
	for i := range imgs {
		out = append(out, toImageXML(&imgs[i]))
	}

	awsquery.WriteXMLResponse(w, describeImagesResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		ImagesSet: out,
	})
}

func toImageXML(img *computedriver.ImageInfo) imageXML {
	state := img.State
	if state == "" {
		state = stateAvailable
	}

	return imageXML{
		ImageID:      img.ID,
		State:        state,
		Name:         img.Name,
		Description:  img.Description,
		CreationDate: img.CreatedAt,
		Architecture: "x86_64",
		Public:       false,
		Tags:         toTagItems(img.Tags),
	}
}

func writeImageErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidAMIID.NotFound", "IncorrectState")
}

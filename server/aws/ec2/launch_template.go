package ec2

import (
	"encoding/xml"
	"net/http"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type launchTemplateXML struct {
	LaunchTemplateID   string `xml:"launchTemplateId"`
	LaunchTemplateName string `xml:"launchTemplateName"`
	Version            int    `xml:"versionNumber"`
	CreateTime         string `xml:"createTime,omitempty"`
}

type createLaunchTemplateResponseXML struct {
	XMLName        xml.Name          `xml:"CreateLaunchTemplateResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate launchTemplateXML `xml:"launchTemplate"`
}

type describeLaunchTemplatesResponseXML struct {
	XMLName         xml.Name            `xml:"DescribeLaunchTemplatesResponse"`
	Xmlns           string              `xml:"xmlns,attr"`
	RequestID       string              `xml:"requestId"`
	LaunchTemplates []launchTemplateXML `xml:"launchTemplates>item"`
}

type deleteLaunchTemplateResponseXML struct {
	XMLName        xml.Name          `xml:"DeleteLaunchTemplateResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate launchTemplateXML `xml:"launchTemplate"`
}

func (h *Handler) createLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	cfg := computedriver.LaunchTemplateConfig{
		Name: r.Form.Get("LaunchTemplateName"),
		InstanceConfig: computedriver.InstanceConfig{
			ImageID:      r.Form.Get("LaunchTemplateData.ImageId"),
			InstanceType: r.Form.Get("LaunchTemplateData.InstanceType"),
		},
	}

	info, err := h.compute.CreateLaunchTemplate(r.Context(), cfg)
	if err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createLaunchTemplateResponseXML{
		Xmlns:          awsquery.Namespace,
		RequestID:      awsquery.RequestID,
		LaunchTemplate: toLaunchTemplateXML(info),
	})
}

func (h *Handler) deleteLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.Form.Get("LaunchTemplateName")
	if name == "" {
		name = r.Form.Get("LaunchTemplateId")
	}

	// Fetch before deleting so the response can echo the deleted template (matches real AWS).
	info, _ := h.compute.GetLaunchTemplate(r.Context(), name)

	if err := h.compute.DeleteLaunchTemplate(r.Context(), name); err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	resp := deleteLaunchTemplateResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
	}
	if info != nil {
		resp.LaunchTemplate = toLaunchTemplateXML(info)
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) describeLaunchTemplates(w http.ResponseWriter, r *http.Request) {
	names := awsquery.ListStrings(r.Form, "LaunchTemplateName")

	var templates []computedriver.LaunchTemplate

	if len(names) == 0 {
		lts, err := h.compute.ListLaunchTemplates(r.Context())
		if err != nil {
			writeLaunchTemplateErr(w, err)
			return
		}

		templates = lts
	} else {
		for _, n := range names {
			lt, err := h.compute.GetLaunchTemplate(r.Context(), n)
			if err != nil {
				continue
			}

			templates = append(templates, *lt)
		}
	}

	out := make([]launchTemplateXML, 0, len(templates))

	for i := range templates {
		out = append(out, toLaunchTemplateXML(&templates[i]))
	}

	awsquery.WriteXMLResponse(w, describeLaunchTemplatesResponseXML{
		Xmlns:           awsquery.Namespace,
		RequestID:       awsquery.RequestID,
		LaunchTemplates: out,
	})
}

func toLaunchTemplateXML(lt *computedriver.LaunchTemplate) launchTemplateXML {
	return launchTemplateXML{
		LaunchTemplateID:   lt.ID,
		LaunchTemplateName: lt.Name,
		Version:            lt.Version,
		CreateTime:         lt.CreatedAt,
	}
}

func writeLaunchTemplateErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidLaunchTemplateId.NotFound", "IncorrectState")
}

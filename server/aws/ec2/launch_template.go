package ec2

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// launchTemplateDataXML is the nested <launchTemplateData> element echoed on
// version responses, carrying the instance parameters a version pins.
type launchTemplateDataXML struct {
	ImageID          string    `xml:"imageId,omitempty"`
	InstanceType     string    `xml:"instanceType,omitempty"`
	KeyName          string    `xml:"keyName,omitempty"`
	SubnetID         string    `xml:"subnetId,omitempty"`
	UserData         string    `xml:"userData,omitempty"`
	SecurityGroupIDs []string  `xml:"securityGroupIdSet>item,omitempty"`
	TagSpec          []tagItem `xml:"tagSpecificationSet>item>tagSet>item,omitempty"`
}

type launchTemplateXML struct {
	LaunchTemplateID     string    `xml:"launchTemplateId"`
	LaunchTemplateName   string    `xml:"launchTemplateName"`
	Version              int       `xml:"versionNumber"`
	DefaultVersionNumber int       `xml:"defaultVersionNumber"`
	LatestVersionNumber  int       `xml:"latestVersionNumber"`
	CreatedBy            string    `xml:"createdBy,omitempty"`
	CreateTime           string    `xml:"createTime,omitempty"`
	Tags                 []tagItem `xml:"tagSet>item,omitempty"`
}

// launchTemplateVersionXML is one <launchTemplateVersion> element.
type launchTemplateVersionXML struct {
	LaunchTemplateID   string                `xml:"launchTemplateId"`
	LaunchTemplateName string                `xml:"launchTemplateName"`
	VersionNumber      int                   `xml:"versionNumber"`
	DefaultVersion     bool                  `xml:"defaultVersion"`
	CreatedBy          string                `xml:"createdBy,omitempty"`
	CreateTime         string                `xml:"createTime,omitempty"`
	VersionDescription string                `xml:"versionDescription,omitempty"`
	LaunchTemplateData launchTemplateDataXML `xml:"launchTemplateData"`
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

type modifyLaunchTemplateResponseXML struct {
	XMLName        xml.Name          `xml:"ModifyLaunchTemplateResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate launchTemplateXML `xml:"launchTemplate"`
}

type createLaunchTemplateVersionResponseXML struct {
	XMLName               xml.Name                 `xml:"CreateLaunchTemplateVersionResponse"`
	Xmlns                 string                   `xml:"xmlns,attr"`
	RequestID             string                   `xml:"requestId"`
	LaunchTemplateVersion launchTemplateVersionXML `xml:"launchTemplateVersion"`
}

type describeLaunchTemplateVersionsResponseXML struct {
	XMLName   xml.Name                   `xml:"DescribeLaunchTemplateVersionsResponse"`
	Xmlns     string                     `xml:"xmlns,attr"`
	RequestID string                     `xml:"requestId"`
	Versions  []launchTemplateVersionXML `xml:"launchTemplateVersionSet>item"`
	NextToken string                     `xml:"nextToken,omitempty"`
}

type getLaunchTemplateDataResponseXML struct {
	XMLName            xml.Name              `xml:"GetLaunchTemplateDataResponse"`
	Xmlns              string                `xml:"xmlns,attr"`
	RequestID          string                `xml:"requestId"`
	LaunchTemplateData launchTemplateDataXML `xml:"launchTemplateData"`
}

func (h *Handler) createLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	cfg := computedriver.LaunchTemplateConfig{
		Name:               r.Form.Get("LaunchTemplateName"),
		InstanceConfig:     parseLaunchTemplateData(r.Form, "LaunchTemplateData"),
		Tags:               mergeTagSpecs(awsquery.TagSpecs(r.Form), "launch-template"),
		VersionDescription: r.Form.Get("VersionDescription"),
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

// modifyLaunchTemplate handles Action=ModifyLaunchTemplate. It promotes the
// version named by SetDefaultVersion to the template's default and echoes the
// updated launch template. Served by the AWS-only LaunchTemplateModifier
// capability.
func (h *Handler) modifyLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	modifier, ok := h.compute.(computedriver.LaunchTemplateModifier)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"ModifyLaunchTemplate is not supported")

		return
	}

	info, err := modifier.ModifyLaunchTemplate(r.Context(), computedriver.ModifyLaunchTemplateInput{
		Name:           r.Form.Get("LaunchTemplateName"),
		ID:             r.Form.Get("LaunchTemplateId"),
		DefaultVersion: r.Form.Get("SetDefaultVersion"),
	})
	if err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyLaunchTemplateResponseXML{
		Xmlns:          awsquery.Namespace,
		RequestID:      awsquery.RequestID,
		LaunchTemplate: toLaunchTemplateXML(info),
	})
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

func (h *Handler) createLaunchTemplateVersion(w http.ResponseWriter, r *http.Request) {
	lt, ok := h.compute.(computedriver.LaunchTemplateVersioner)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"CreateLaunchTemplateVersion is not supported")

		return
	}

	ver, err := lt.CreateLaunchTemplateVersion(r.Context(), computedriver.CreateLaunchTemplateVersionInput{
		Name:               r.Form.Get("LaunchTemplateName"),
		ID:                 r.Form.Get("LaunchTemplateId"),
		SourceVersion:      r.Form.Get("SourceVersion"),
		VersionDescription: r.Form.Get("VersionDescription"),
		InstanceConfig:     parseLaunchTemplateData(r.Form, "LaunchTemplateData"),
	})
	if err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createLaunchTemplateVersionResponseXML{
		Xmlns:                 awsquery.Namespace,
		RequestID:             awsquery.RequestID,
		LaunchTemplateVersion: toLaunchTemplateVersionXML(ver),
	})
}

func (h *Handler) describeLaunchTemplateVersions(w http.ResponseWriter, r *http.Request) {
	lt, ok := h.compute.(computedriver.LaunchTemplateVersioner)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"DescribeLaunchTemplateVersions is not supported")

		return
	}

	versions, err := lt.DescribeLaunchTemplateVersions(r.Context(), computedriver.DescribeLaunchTemplateVersionsInput{
		Name:       r.Form.Get("LaunchTemplateName"),
		ID:         r.Form.Get("LaunchTemplateId"),
		Versions:   awsquery.ListStrings(r.Form, "LaunchTemplateVersion"),
		MinVersion: r.Form.Get("MinVersion"),
		MaxVersion: r.Form.Get("MaxVersion"),
	})
	if err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	page, next := paginateXML(versions, r.Form.Get("MaxResults"), r.Form.Get("NextToken"),
		func(v computedriver.LaunchTemplateVersion) string { return strconv.Itoa(v.VersionNumber) })

	out := make([]launchTemplateVersionXML, 0, len(page))
	for i := range page {
		out = append(out, toLaunchTemplateVersionXML(&page[i]))
	}

	awsquery.WriteXMLResponse(w, describeLaunchTemplateVersionsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Versions:  out,
		NextToken: next,
	})
}

func (h *Handler) getLaunchTemplateData(w http.ResponseWriter, r *http.Request) {
	lt, ok := h.compute.(computedriver.LaunchTemplateVersioner)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"GetLaunchTemplateData is not supported")

		return
	}

	cfg, err := lt.GetLaunchTemplateData(r.Context(), r.Form.Get("InstanceId"))
	if err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getLaunchTemplateDataResponseXML{
		Xmlns:              awsquery.Namespace,
		RequestID:          awsquery.RequestID,
		LaunchTemplateData: toLaunchTemplateDataXML(*cfg),
	})
}

// parseLaunchTemplateData reads the LaunchTemplateData.* parameter group into an
// InstanceConfig. prefix is "LaunchTemplateData".
func parseLaunchTemplateData(form url.Values, prefix string) computedriver.InstanceConfig {
	return computedriver.InstanceConfig{
		ImageID:        form.Get(prefix + ".ImageId"),
		InstanceType:   form.Get(prefix + ".InstanceType"),
		KeyName:        form.Get(prefix + ".KeyName"),
		SubnetID:       form.Get(prefix + ".SubnetId"),
		UserData:       form.Get(prefix + ".UserData"),
		SecurityGroups: awsquery.ListStrings(form, prefix+".SecurityGroupId"),
	}
}

func toLaunchTemplateXML(lt *computedriver.LaunchTemplate) launchTemplateXML {
	return launchTemplateXML{
		LaunchTemplateID:     lt.ID,
		LaunchTemplateName:   lt.Name,
		Version:              lt.Version,
		DefaultVersionNumber: lt.DefaultVersion,
		LatestVersionNumber:  lt.LatestVersion,
		CreatedBy:            lt.CreatedBy,
		CreateTime:           lt.CreatedAt,
		Tags:                 toTagItems(lt.Tags),
	}
}

func toLaunchTemplateVersionXML(v *computedriver.LaunchTemplateVersion) launchTemplateVersionXML {
	return launchTemplateVersionXML{
		LaunchTemplateID:   v.LaunchTemplateID,
		LaunchTemplateName: v.LaunchTemplateName,
		VersionNumber:      v.VersionNumber,
		DefaultVersion:     v.DefaultVersion,
		CreatedBy:          v.CreatedBy,
		CreateTime:         v.CreateTime,
		VersionDescription: v.VersionDescription,
		LaunchTemplateData: toLaunchTemplateDataXML(v.InstanceConfig),
	}
}

//nolint:gocritic // hugeParam: value copy of a small config for read-only projection.
func toLaunchTemplateDataXML(cfg computedriver.InstanceConfig) launchTemplateDataXML {
	return launchTemplateDataXML{
		ImageID:          cfg.ImageID,
		InstanceType:     cfg.InstanceType,
		KeyName:          cfg.KeyName,
		SubnetID:         cfg.SubnetID,
		UserData:         cfg.UserData,
		SecurityGroupIDs: cfg.SecurityGroups,
		TagSpec:          toTagItems(cfg.Tags),
	}
}

// writeLaunchTemplateErr maps a duplicate create to the EC2-specific
// InvalidLaunchTemplateName.AlreadyExistsException code rather than the generic
// ResourceAlreadyExists; other codes use the shared mapping.
func writeLaunchTemplateErr(w http.ResponseWriter, err error) {
	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidLaunchTemplateName.AlreadyExistsException", cerrors.Message(err))

		return
	}

	writeErrWithNotFound(w, err, "InvalidLaunchTemplateId.NotFound", "IncorrectState")
}

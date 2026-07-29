package rds

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- XML shapes ----

type optionXML struct {
	OptionName    string `xml:"OptionName"`
	Port          int    `xml:"Port,omitempty"`
	OptionVersion string `xml:"OptionVersion,omitempty"`
}

type optionGroupXML struct {
	OptionGroupName            string      `xml:"OptionGroupName"`
	EngineName                 string      `xml:"EngineName,omitempty"`
	MajorEngineVersion         string      `xml:"MajorEngineVersion,omitempty"`
	OptionGroupDescription     string      `xml:"OptionGroupDescription,omitempty"`
	OptionGroupArn             string      `xml:"OptionGroupArn,omitempty"`
	AllowsVpcAndNonVpcInstance bool        `xml:"AllowsVpcAndNonVpcInstanceMemberships"`
	Options                    []optionXML `xml:"Options>Option,omitempty"`
}

type optionGroupResult struct {
	OptionGroup optionGroupXML `xml:"OptionGroup"`
}

type createOptionGroupResponse struct {
	XMLName  xml.Name          `xml:"CreateOptionGroupResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   optionGroupResult `xml:"CreateOptionGroupResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type modifyOptionGroupResponse struct {
	XMLName  xml.Name          `xml:"ModifyOptionGroupResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   optionGroupResult `xml:"ModifyOptionGroupResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type copyOptionGroupResponse struct {
	XMLName  xml.Name          `xml:"CopyOptionGroupResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   optionGroupResult `xml:"CopyOptionGroupResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type describeOptionGroupsResponse struct {
	XMLName  xml.Name         `xml:"DescribeOptionGroupsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   optionGroupsList `xml:"DescribeOptionGroupsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type optionGroupsList struct {
	OptionGroupsList []optionGroupXML `xml:"OptionGroupsList>OptionGroup"`
}

type deleteOptionGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteOptionGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type optionGroupOptionXML struct {
	Name               string `xml:"Name"`
	Description        string `xml:"Description,omitempty"`
	EngineName         string `xml:"EngineName,omitempty"`
	MajorEngineVersion string `xml:"MajorEngineVersion,omitempty"`
	Persistent         bool   `xml:"Persistent"`
	Permanent          bool   `xml:"Permanent"`
}

type describeOptionGroupOptionsResponse struct {
	XMLName  xml.Name               `xml:"DescribeOptionGroupOptionsResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   optionGroupOptionsList `xml:"DescribeOptionGroupOptionsResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type optionGroupOptionsList struct {
	OptionGroupOptions []optionGroupOptionXML `xml:"OptionGroupOptions>OptionGroupOption"`
}

// ---- helpers ----

func (h *Handler) optionGroupsCap() (rdsdriver.OptionGroups, bool) {
	og, ok := h.db.(rdsdriver.OptionGroups)

	return og, ok
}

// parseOptionsToInclude reads
// OptionsToInclude.OptionConfiguration.N.{OptionName,Port,OptionVersion}. RDS
// names the list member after its type (OptionConfiguration), not "member".
func parseOptionsToInclude(form url.Values) []rdsdriver.Option {
	indices := awsquery.CollectIndices(form, "OptionsToInclude.OptionConfiguration")
	if len(indices) == 0 {
		return nil
	}

	out := make([]rdsdriver.Option, 0, len(indices))

	for _, n := range indices {
		base := "OptionsToInclude.OptionConfiguration." + strconv.Itoa(n)

		name := form.Get(base + ".OptionName")
		if name == "" {
			continue
		}

		out = append(out, rdsdriver.Option{
			Name:    name,
			Port:    formInt(form.Get(base + ".Port")),
			Version: form.Get(base + ".OptionVersion"),
		})
	}

	return out
}

func toOptionGroupXML(og *rdsdriver.OptionGroup) optionGroupXML {
	x := optionGroupXML{
		OptionGroupName:            og.Name,
		EngineName:                 og.EngineName,
		MajorEngineVersion:         og.MajorEngineVersion,
		OptionGroupDescription:     og.Description,
		OptionGroupArn:             og.ARN,
		AllowsVpcAndNonVpcInstance: true,
	}

	for _, o := range og.Options {
		x.Options = append(x.Options, optionXML{
			OptionName:    o.Name,
			Port:          o.Port,
			OptionVersion: o.Version,
		})
	}

	return x
}

// ---- handlers ----

func (h *Handler) createOptionGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.optionGroupsCap()
	if !ok {
		writeUnsupported(w, "option groups")
		return
	}

	og, err := store.CreateOptionGroup(r.Context(), rdsdriver.OptionGroupConfig{
		Name:               r.Form.Get("OptionGroupName"),
		EngineName:         r.Form.Get("EngineName"),
		MajorEngineVersion: r.Form.Get("MajorEngineVersion"),
		Description:        r.Form.Get("OptionGroupDescription"),
		Tags:               parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createOptionGroupResponse{
		Xmlns:    Namespace,
		Result:   optionGroupResult{OptionGroup: toOptionGroupXML(og)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeOptionGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.optionGroupsCap()
	if !ok {
		writeUnsupported(w, "option groups")
		return
	}

	var names []string
	if n := r.Form.Get("OptionGroupName"); n != "" {
		names = []string{n}
	}

	groups, err := store.DescribeOptionGroups(r.Context(), names, r.Form.Get("EngineName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]optionGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, toOptionGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, describeOptionGroupsResponse{
		Xmlns:    Namespace,
		Result:   optionGroupsList{OptionGroupsList: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyOptionGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.optionGroupsCap()
	if !ok {
		writeUnsupported(w, "option groups")
		return
	}

	og, err := store.ModifyOptionGroup(r.Context(),
		r.Form.Get("OptionGroupName"),
		parseOptionsToInclude(r.Form),
		awsquery.ListStrings(r.Form, "OptionsToRemove.member"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyOptionGroupResponse{
		Xmlns:    Namespace,
		Result:   optionGroupResult{OptionGroup: toOptionGroupXML(og)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteOptionGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.optionGroupsCap()
	if !ok {
		writeUnsupported(w, "option groups")
		return
	}

	if err := store.DeleteOptionGroup(r.Context(), r.Form.Get("OptionGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteOptionGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) copyOptionGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.optionGroupsCap()
	if !ok {
		writeUnsupported(w, "option groups")
		return
	}

	og, err := store.CopyOptionGroup(r.Context(),
		r.Form.Get("SourceOptionGroupIdentifier"),
		r.Form.Get("TargetOptionGroupIdentifier"),
		r.Form.Get("TargetOptionGroupDescription"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copyOptionGroupResponse{
		Xmlns:    Namespace,
		Result:   optionGroupResult{OptionGroup: toOptionGroupXML(og)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeOptionGroupOptions(w http.ResponseWriter, r *http.Request) {
	store, ok := h.optionGroupsCap()
	if !ok {
		writeUnsupported(w, "option groups")
		return
	}

	opts, err := store.DescribeOptionGroupOptions(r.Context(),
		r.Form.Get("EngineName"), r.Form.Get("MajorEngineVersion"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]optionGroupOptionXML, 0, len(opts))
	for _, o := range opts {
		out = append(out, optionGroupOptionXML{
			Name:               o.Name,
			Description:        o.Description,
			EngineName:         o.EngineName,
			MajorEngineVersion: o.MajorEngineVersion,
			Persistent:         o.Persistent,
			Permanent:          o.Permanent,
		})
	}

	awsquery.WriteXMLResponse(w, describeOptionGroupOptionsResponse{
		Xmlns:    Namespace,
		Result:   optionGroupOptionsList{OptionGroupOptions: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

package cloudformation

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// The export, protection and account actions.
const (
	actionListExports                 = "ListExports"
	actionListImports                 = "ListImports"
	actionUpdateTerminationProtection = "UpdateTerminationProtection"
	actionDescribeAccountLimits       = "DescribeAccountLimits"
	actionEstimateTemplateCost        = "EstimateTemplateCost"
)

// serveAccount dispatches the export, protection and account actions.
func (h *Handler) serveAccount(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case actionListExports:
		h.listExports(w, r)
	case actionListImports:
		h.listImports(w, r)
	case actionUpdateTerminationProtection:
		h.updateTerminationProtection(w, r)
	case actionDescribeAccountLimits:
		h.describeAccountLimits(w, r)
	case actionEstimateTemplateCost:
		h.estimateTemplateCost(w, r)
	}
}

func (h *Handler) listExports(w http.ResponseWriter, r *http.Request) {
	list, err := h.api.ListExports(r.Context(), r.Form.Get("NextToken"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp listExportsResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()
	resp.Result.NextToken = list.NextToken

	for _, e := range list.Exports {
		resp.Result.Exports = append(resp.Result.Exports, exportXML{
			ExportingStackID: e.ExportingStackID, Name: e.Name, Value: e.Value,
		})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) listImports(w http.ResponseWriter, r *http.Request) {
	list, err := h.api.ListImports(r.Context(), &cfn.ListImportsInput{
		ExportName: r.Form.Get("ExportName"),
		NextToken:  r.Form.Get("NextToken"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp listImportsResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()
	resp.Result.Imports = list.Imports
	resp.Result.NextToken = list.NextToken

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) updateTerminationProtection(w http.ResponseWriter, r *http.Request) {
	id, err := h.api.UpdateTerminationProtection(r.Context(), &cfn.UpdateTerminationProtectionInput{
		StackName: r.Form.Get("StackName"),
		Enable:    formBool(r.Form, "EnableTerminationProtection"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateTerminationProtectionResponse{
		Xmlns: Namespace, Result: stackIDResult{StackID: id}, Meta: meta(),
	})
}

func (h *Handler) describeAccountLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := h.api.DescribeAccountLimits(r.Context(), r.Form.Get("NextToken"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp describeAccountLimitsResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()

	for _, l := range limits {
		resp.Result.AccountLimits = append(resp.Result.AccountLimits, accountLimitXML{Name: l.Name, Value: l.Value})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) estimateTemplateCost(w http.ResponseWriter, r *http.Request) {
	u, err := h.api.EstimateTemplateCost(r.Context(), &cfn.EstimateTemplateCostInput{
		TemplateBody: r.Form.Get("TemplateBody"),
		TemplateURL:  r.Form.Get("TemplateURL"),
		Parameters:   parseParameters(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp estimateTemplateCostResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()
	resp.Result.URL = u

	awsquery.WriteXMLResponse(w, resp)
}

type exportXML struct {
	ExportingStackID string `xml:"ExportingStackId"`
	Name             string `xml:"Name"`
	Value            string `xml:"Value"`
}

type listExportsResponse struct {
	XMLName xml.Name `xml:"ListExportsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Exports   []exportXML `xml:"Exports>member"`
		NextToken string      `xml:"NextToken,omitempty"`
	} `xml:"ListExportsResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type listImportsResponse struct {
	XMLName xml.Name `xml:"ListImportsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Imports   []string `xml:"Imports>member"`
		NextToken string   `xml:"NextToken,omitempty"`
	} `xml:"ListImportsResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type updateTerminationProtectionResponse struct {
	XMLName xml.Name         `xml:"UpdateTerminationProtectionResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  stackIDResult    `xml:"UpdateTerminationProtectionResult"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type accountLimitXML struct {
	Name  string `xml:"Name"`
	Value int    `xml:"Value"`
}

type describeAccountLimitsResponse struct {
	XMLName xml.Name `xml:"DescribeAccountLimitsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		AccountLimits []accountLimitXML `xml:"AccountLimits>member"`
	} `xml:"DescribeAccountLimitsResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type estimateTemplateCostResponse struct {
	XMLName xml.Name `xml:"EstimateTemplateCostResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		URL string `xml:"Url"`
	} `xml:"EstimateTemplateCostResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

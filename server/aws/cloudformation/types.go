package cloudformation

import (
	"encoding/xml"
	"net/url"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// responseMetadata is the trailing <ResponseMetadata><RequestId/> every query
// response carries.
type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

func meta() responseMetadata { return responseMetadata{RequestID: awsquery.RequestID} }

// isoTime formats t the way CloudFormation renders timestamps; a zero time
// yields "".
func isoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// --- request parsing ---

// createInput reads a CreateStack form into the service input.
func createInput(form url.Values) cfn.CreateStackInput {
	return cfn.CreateStackInput{
		StackName:    form.Get("StackName"),
		TemplateBody: form.Get("TemplateBody"),
		Parameters:   parseParameters(form),
		Tags:         parseTags(form),
		Capabilities: awsquery.ListStrings(form, "Capabilities.member"),
	}
}

func updateInput(form url.Values) cfn.UpdateStackInput {
	return cfn.UpdateStackInput{
		StackName:    form.Get("StackName"),
		TemplateBody: form.Get("TemplateBody"),
		Parameters:   parseParameters(form),
		Tags:         parseTags(form),
		Capabilities: awsquery.ListStrings(form, "Capabilities.member"),
	}
}

func parseParameters(form url.Values) []cfn.Parameter {
	idx := awsquery.CollectIndices(form, "Parameters.member")
	if len(idx) == 0 {
		return nil
	}

	out := make([]cfn.Parameter, 0, len(idx))

	for _, i := range idx {
		base := "Parameters.member." + strconv.Itoa(i)

		key := form.Get(base + ".ParameterKey")
		if key == "" {
			continue
		}

		out = append(out, cfn.Parameter{Key: key, Value: form.Get(base + ".ParameterValue")})
	}

	return out
}

func parseTags(form url.Values) map[string]string {
	idx := awsquery.CollectIndices(form, "Tags.member")
	if len(idx) == 0 {
		return nil
	}

	out := make(map[string]string, len(idx))

	for _, i := range idx {
		base := "Tags.member." + strconv.Itoa(i)

		key := form.Get(base + ".Key")
		if key != "" {
			out[key] = form.Get(base + ".Value")
		}
	}

	return out
}

// --- response DTOs ---

type stackIDResult struct {
	StackID string `xml:"StackId"`
}

type createStackResponse struct {
	XMLName xml.Name         `xml:"CreateStackResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  stackIDResult    `xml:"CreateStackResult"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type updateStackResponse struct {
	XMLName xml.Name         `xml:"UpdateStackResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  stackIDResult    `xml:"UpdateStackResult"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type deleteStackResponse struct {
	XMLName xml.Name         `xml:"DeleteStackResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type parameterXML struct {
	ParameterKey   string `xml:"ParameterKey"`
	ParameterValue string `xml:"ParameterValue"`
}

type outputXML struct {
	OutputKey   string `xml:"OutputKey"`
	OutputValue string `xml:"OutputValue"`
	Description string `xml:"Description,omitempty"`
	ExportName  string `xml:"ExportName,omitempty"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type stackXML struct {
	StackID           string         `xml:"StackId"`
	StackName         string         `xml:"StackName"`
	Description       string         `xml:"Description,omitempty"`
	CreationTime      string         `xml:"CreationTime"`
	LastUpdatedTime   string         `xml:"LastUpdatedTime,omitempty"`
	DeletionTime      string         `xml:"DeletionTime,omitempty"`
	StackStatus       string         `xml:"StackStatus"`
	StackStatusReason string         `xml:"StackStatusReason,omitempty"`
	DisableRollback   bool           `xml:"DisableRollback"`
	Parameters        []parameterXML `xml:"Parameters>member,omitempty"`
	Outputs           []outputXML    `xml:"Outputs>member,omitempty"`
	Tags              []tagXML       `xml:"Tags>member,omitempty"`
	Capabilities      []string       `xml:"Capabilities>member,omitempty"`
}

type describeStacksResponse struct {
	XMLName xml.Name `xml:"DescribeStacksResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Stacks []stackXML `xml:"Stacks>member"`
	} `xml:"DescribeStacksResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type eventXML struct {
	StackID              string `xml:"StackId"`
	EventID              string `xml:"EventId"`
	StackName            string `xml:"StackName"`
	LogicalResourceID    string `xml:"LogicalResourceId"`
	PhysicalResourceID   string `xml:"PhysicalResourceId"`
	ResourceType         string `xml:"ResourceType"`
	Timestamp            string `xml:"Timestamp"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
}

type describeStackEventsResponse struct {
	XMLName xml.Name `xml:"DescribeStackEventsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		StackEvents []eventXML `xml:"StackEvents>member"`
	} `xml:"DescribeStackEventsResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type summaryXML struct {
	StackID             string `xml:"StackId"`
	StackName           string `xml:"StackName"`
	TemplateDescription string `xml:"TemplateDescription,omitempty"`
	CreationTime        string `xml:"CreationTime"`
	LastUpdatedTime     string `xml:"LastUpdatedTime,omitempty"`
	DeletionTime        string `xml:"DeletionTime,omitempty"`
	StackStatus         string `xml:"StackStatus"`
	StackStatusReason   string `xml:"StackStatusReason,omitempty"`
}

type listStacksResponse struct {
	XMLName xml.Name `xml:"ListStacksResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		StackSummaries []summaryXML `xml:"StackSummaries>member"`
	} `xml:"ListStacksResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type resourceXML struct {
	StackID              string `xml:"StackId"`
	StackName            string `xml:"StackName"`
	LogicalResourceID    string `xml:"LogicalResourceId"`
	PhysicalResourceID   string `xml:"PhysicalResourceId"`
	ResourceType         string `xml:"ResourceType"`
	Timestamp            string `xml:"Timestamp"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
}

type describeStackResourcesResponse struct {
	XMLName xml.Name `xml:"DescribeStackResourcesResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		StackResources []resourceXML `xml:"StackResources>member"`
	} `xml:"DescribeStackResourcesResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type resourceSummaryXML struct {
	LogicalResourceID    string `xml:"LogicalResourceId"`
	PhysicalResourceID   string `xml:"PhysicalResourceId"`
	ResourceType         string `xml:"ResourceType"`
	LastUpdatedTimestamp string `xml:"LastUpdatedTimestamp"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
}

type listStackResourcesResponse struct {
	XMLName xml.Name `xml:"ListStackResourcesResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		StackResourceSummaries []resourceSummaryXML `xml:"StackResourceSummaries>member"`
	} `xml:"ListStackResourcesResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type getTemplateResponse struct {
	XMLName xml.Name `xml:"GetTemplateResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		TemplateBody string `xml:"TemplateBody"`
	} `xml:"GetTemplateResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

// --- mapping helpers ---

func toStackXML(s *cfn.Stack) stackXML {
	x := stackXML{
		StackID: s.ID, StackName: s.Name, Description: s.Description,
		CreationTime: isoTime(s.CreationTime), LastUpdatedTime: isoTime(s.LastUpdated),
		StackStatus: s.Status, StackStatusReason: s.StatusReason,
		Capabilities: s.Capabilities,
	}

	for _, p := range s.Parameters {
		x.Parameters = append(x.Parameters, parameterXML{ParameterKey: p.Key, ParameterValue: p.Value})
	}

	for _, o := range s.Outputs {
		x.Outputs = append(x.Outputs, outputXML{
			OutputKey: o.Key, OutputValue: o.Value, Description: o.Description, ExportName: o.ExportName,
		})
	}

	for k, v := range s.Tags {
		x.Tags = append(x.Tags, tagXML{Key: k, Value: v})
	}

	return x
}

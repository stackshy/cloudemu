package streamanalytics

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/streamanalytics"
)

// jobRequest is the ARM streaming-job PUT/PATCH body. location and tags are
// top-level; the rest live under properties.
type jobRequest struct {
	Location   string                `json:"location"`
	Tags       map[string]string     `json:"tags,omitempty"`
	Properties *jobPropertiesRequest `json:"properties,omitempty"`
}

// jobPropertiesRequest is the writable subset of job properties. The embedded
// transformation/inputs/outputs/functions are honored only on a PUT create; a
// PATCH cannot modify them (each is managed via its own sub-resource endpoint).
type jobPropertiesRequest struct {
	Sku                                *skuWire    `json:"sku,omitempty"`
	EventsOutOfOrderPolicy             *string     `json:"eventsOutOfOrderPolicy,omitempty"`
	OutputErrorPolicy                  *string     `json:"outputErrorPolicy,omitempty"`
	EventsOutOfOrderMaxDelayInSeconds  *int        `json:"eventsOutOfOrderMaxDelayInSeconds,omitempty"`
	EventsLateArrivalMaxDelayInSeconds *int        `json:"eventsLateArrivalMaxDelayInSeconds,omitempty"`
	DataLocale                         *string     `json:"dataLocale,omitempty"`
	CompatibilityLevel                 *string     `json:"compatibilityLevel,omitempty"`
	JobType                            *string     `json:"jobType,omitempty"`
	OutputStartMode                    *string     `json:"outputStartMode,omitempty"`
	OutputStartTime                    *string     `json:"outputStartTime,omitempty"`
	Transformation                     *childBody  `json:"transformation,omitempty"`
	Inputs                             []childBody `json:"inputs,omitempty"`
	Outputs                            []childBody `json:"outputs,omitempty"`
	Functions                          []childBody `json:"functions,omitempty"`
}

// skuWire is the streaming-job SKU block; only the name is meaningful.
type skuWire struct {
	Name string `json:"name,omitempty"`
}

// childBody is the wire shape of an embedded or standalone child: a name plus a
// raw properties block that round-trips verbatim.
type childBody struct {
	Name       string          `json:"name,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// jobResponse is the ARM representation of a streaming job.
type jobResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties jobPropertiesResp `json:"properties"`
}

// jobPropertiesResp is the job properties block. The computed fields (jobId,
// provisioningState, jobState, createdDate, etag) are stable across reads.
type jobPropertiesResp struct {
	Sku                                skuWire         `json:"sku"`
	JobID                              string          `json:"jobId"`
	ProvisioningState                  string          `json:"provisioningState"`
	JobState                           string          `json:"jobState"`
	EventsOutOfOrderPolicy             string          `json:"eventsOutOfOrderPolicy,omitempty"`
	OutputErrorPolicy                  string          `json:"outputErrorPolicy,omitempty"`
	EventsOutOfOrderMaxDelayInSeconds  *int            `json:"eventsOutOfOrderMaxDelayInSeconds,omitempty"`
	EventsLateArrivalMaxDelayInSeconds *int            `json:"eventsLateArrivalMaxDelayInSeconds,omitempty"`
	DataLocale                         string          `json:"dataLocale,omitempty"`
	CreatedDate                        string          `json:"createdDate"`
	CompatibilityLevel                 string          `json:"compatibilityLevel,omitempty"`
	JobType                            string          `json:"jobType,omitempty"`
	OutputStartMode                    string          `json:"outputStartMode,omitempty"`
	OutputStartTime                    string          `json:"outputStartTime,omitempty"`
	Etag                               string          `json:"etag"`
	Transformation                     *childResponse  `json:"transformation,omitempty"`
	Inputs                             []childResponse `json:"inputs,omitempty"`
	Outputs                            []childResponse `json:"outputs,omitempty"`
	Functions                          []childResponse `json:"functions,omitempty"`
}

// jobListResponse is the ARM job list envelope. nextLink is omitted — the
// emulator returns a single page.
type jobListResponse struct {
	Value []jobResponse `json:"value"`
}

// childResponse is the ARM representation of a transformation/input/output/
// function. Its properties block carries the stored (verbatim) properties with
// the stable etag injected.
type childResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// childListResponse is the ARM child list envelope.
type childListResponse struct {
	Value []childResponse `json:"value"`
}

// startRequest is the start-action POST body.
type startRequest struct {
	OutputStartMode string `json:"outputStartMode,omitempty"`
	OutputStartTime string `json:"outputStartTime,omitempty"`
}

// scaleRequest is the scale-action POST body.
type scaleRequest struct {
	StreamingUnits *int `json:"streamingUnits,omitempty"`
}

// testStatusResponse is the ResourceTestStatus body a datasource test returns.
type testStatusResponse struct {
	Status string `json:"status"`
}

// jobInputFromRequest builds a job create/update Input from a request body.
// Pointer fields are carried through so an absent field falls back to the stored
// (or default) value in the driver, which makes a PATCH body merge on its own.
func jobInputFromRequest(req *jobRequest) streamanalytics.JobInput {
	in := streamanalytics.JobInput{Tags: req.Tags}

	p := req.Properties
	if p == nil {
		return in
	}

	if p.Sku != nil {
		in.SkuName = &p.Sku.Name
	}

	in.EventsOutOfOrderPolicy = p.EventsOutOfOrderPolicy
	in.OutputErrorPolicy = p.OutputErrorPolicy
	in.EventsOutOfOrderMaxDelayInSeconds = p.EventsOutOfOrderMaxDelayInSeconds
	in.EventsLateArrivalMaxDelayInSeconds = p.EventsLateArrivalMaxDelayInSeconds
	in.DataLocale = p.DataLocale
	in.CompatibilityLevel = p.CompatibilityLevel
	in.JobType = p.JobType
	in.OutputStartMode = p.OutputStartMode
	in.OutputStartTime = p.OutputStartTime

	return in
}

// toJobResponse projects a stored job and its children onto the ARM wire
// representation, embedding the transformation (single) and the inputs/outputs/
// functions collections.
func toJobResponse(j *streamanalytics.StreamingJob, children []streamanalytics.Child) jobResponse {
	out := jobResponse{
		ID:       j.ARMID(),
		Name:     j.Name,
		Type:     jobArmType,
		Location: j.Location,
		Tags:     j.Tags,
		Properties: jobPropertiesResp{
			Sku:                                skuWire{Name: j.SkuName},
			JobID:                              j.JobID,
			ProvisioningState:                  j.ProvisioningState,
			JobState:                           j.JobState,
			EventsOutOfOrderPolicy:             j.EventsOutOfOrderPolicy,
			OutputErrorPolicy:                  j.OutputErrorPolicy,
			EventsOutOfOrderMaxDelayInSeconds:  j.EventsOutOfOrderMaxDelayInSeconds,
			EventsLateArrivalMaxDelayInSeconds: j.EventsLateArrivalMaxDelayInSeconds,
			DataLocale:                         j.DataLocale,
			CreatedDate:                        j.CreatedDate,
			CompatibilityLevel:                 j.CompatibilityLevel,
			JobType:                            j.JobType,
			OutputStartMode:                    j.OutputStartMode,
			OutputStartTime:                    j.OutputStartTime,
			Etag:                               j.Etag,
		},
	}

	embedChildren(&out.Properties, children)

	return out
}

// embedChildren groups a job's children by kind and attaches them to the job
// properties block.
func embedChildren(p *jobPropertiesResp, children []streamanalytics.Child) {
	for i := range children {
		c := &children[i]
		resp := toChildResponse(c)

		switch c.Kind {
		case streamanalytics.KindTransformations:
			t := resp
			p.Transformation = &t
		case streamanalytics.KindInputs:
			p.Inputs = append(p.Inputs, resp)
		case streamanalytics.KindOutputs:
			p.Outputs = append(p.Outputs, resp)
		case streamanalytics.KindFunctions:
			p.Functions = append(p.Functions, resp)
		}
	}
}

// toChildResponse projects a stored child onto its ARM wire representation, with
// the stable etag injected into the properties block.
func toChildResponse(c *streamanalytics.Child) childResponse {
	return childResponse{
		ID:         c.ARMID(),
		Name:       c.Name,
		Type:       c.ARMType(),
		Properties: propertiesWithEtag(c.Properties, c.Etag),
	}
}

// propertiesWithEtag returns the stored raw properties with the etag field set.
// A malformed or empty properties block yields an object carrying just the etag,
// so the wire always shows the stable etag.
func propertiesWithEtag(properties json.RawMessage, etag string) json.RawMessage {
	obj := map[string]json.RawMessage{}
	if len(properties) > 0 {
		if err := json.Unmarshal(properties, &obj); err != nil {
			obj = map[string]json.RawMessage{}
		}
	}

	etagJSON, err := json.Marshal(etag)
	if err != nil {
		return properties
	}

	obj["etag"] = etagJSON

	raw, err := json.Marshal(obj)
	if err != nil {
		return properties
	}

	return raw
}

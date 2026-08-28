package iam

import (
	"context"
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// policySimulator is the AWS-specific policy-simulation surface. It's not part
// of the portable IAM driver, so the handler type-asserts for it.
type policySimulator interface {
	SimulatePrincipalPolicy(
		ctx context.Context, policySourceARN string, actions, resourceARNs, extraPolicies []string, condCtx map[string]string,
	) ([]iamdriver.SimulationResult, error)
	SimulateCustomPolicy(
		ctx context.Context, policyDocs, actions, resourceARNs []string, condCtx map[string]string,
	) ([]iamdriver.SimulationResult, error)
}

type evaluationResultXML struct {
	EvalActionName   string `xml:"EvalActionName"`
	EvalResourceName string `xml:"EvalResourceName,omitempty"`
	EvalDecision     string `xml:"EvalDecision"`
}

type evaluationResultsXML struct {
	Member []evaluationResultXML `xml:"member,omitempty"`
}

type simulateResult struct {
	EvaluationResults evaluationResultsXML `xml:"EvaluationResults"`
	IsTruncated       bool                 `xml:"IsTruncated"`
}

type simulatePrincipalPolicyResponse struct {
	XMLName  xml.Name         `xml:"SimulatePrincipalPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   simulateResult   `xml:"SimulatePrincipalPolicyResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type simulateCustomPolicyResponse struct {
	XMLName  xml.Name         `xml:"SimulateCustomPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   simulateResult   `xml:"SimulateCustomPolicyResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// toEvaluationResults maps driver simulation results to the wire shape.
func toEvaluationResults(results []iamdriver.SimulationResult) evaluationResultsXML {
	out := evaluationResultsXML{Member: make([]evaluationResultXML, 0, len(results))}
	for i := range results {
		out.Member = append(out.Member, evaluationResultXML{
			EvalActionName:   results[i].ActionName,
			EvalResourceName: results[i].ResourceName,
			EvalDecision:     results[i].Decision,
		})
	}

	return out
}

func (h *Handler) simulatePrincipalPolicy(w http.ResponseWriter, r *http.Request) {
	sim, ok := h.iam.(policySimulator)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "policy simulation not supported")
		return
	}

	actions := awsquery.ListStrings(r.Form, "ActionNames.member")
	if len(actions) == 0 {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidInput", "ActionNames is required")
		return
	}

	resources := awsquery.ListStrings(r.Form, "ResourceArns.member")
	extra := awsquery.ListStrings(r.Form, "PolicyInputList.member")
	condCtx := parseContextEntries(r)

	results, err := sim.SimulatePrincipalPolicy(r.Context(), r.Form.Get("PolicySourceArn"), actions, resources, extra, condCtx)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, simulatePrincipalPolicyResponse{
		Xmlns:    Namespace,
		Result:   simulateResult{EvaluationResults: toEvaluationResults(results)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseContextEntries reads the SimulatePolicy ContextEntries.member.N groups
// (ContextKeyName + ContextKeyValues.member.M) into a flat key→value condition
// context, taking the first value of each key. It stops at the first absent
// index, matching the contiguous 1..N form SDKs emit.
func parseContextEntries(r *http.Request) map[string]string {
	const maxEntries = 64

	out := map[string]string{}

	for i := 1; i <= maxEntries; i++ {
		base := "ContextEntries.member." + strconv.Itoa(i)

		name := r.Form.Get(base + ".ContextKeyName")
		if name == "" {
			break
		}

		if vals := awsquery.ListStrings(r.Form, base+".ContextKeyValues.member"); len(vals) > 0 {
			out[name] = vals[0]
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func (h *Handler) simulateCustomPolicy(w http.ResponseWriter, r *http.Request) {
	sim, ok := h.iam.(policySimulator)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "policy simulation not supported")
		return
	}

	actions := awsquery.ListStrings(r.Form, "ActionNames.member")
	docs := awsquery.ListStrings(r.Form, "PolicyInputList.member")

	if len(actions) == 0 || len(docs) == 0 {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidInput", "ActionNames and PolicyInputList are required")
		return
	}

	resources := awsquery.ListStrings(r.Form, "ResourceArns.member")
	condCtx := parseContextEntries(r)

	results, err := sim.SimulateCustomPolicy(r.Context(), docs, actions, resources, condCtx)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, simulateCustomPolicyResponse{
		Xmlns:    Namespace,
		Result:   simulateResult{EvaluationResults: toEvaluationResults(results)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"maps"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// lambdaMessageVersion is the messageVersion of the agent-to-Lambda event.
const lambdaMessageVersion = "1.0"

// actionCall is the action an agent turn decided to run.
type actionCall struct {
	group *actionGroup
	input driver.InvocationInput
}

func (c *actionCall) name() string {
	if c.input.Function != "" {
		return c.input.Function
	}

	return c.input.HTTPMethod + " " + c.input.APIPath
}

// pickAction chooses the action the input asks for. The emulator has no model
// to plan with, so it picks the first function whose name (with "_" read as a
// space) or API path the input mentions; parameters are taken from
// "name=value" or "name: value" in the input.
func pickAction(groups []actionGroup, input string) *actionCall {
	text := strings.ToLower(input)

	for i := range groups {
		g := &groups[i]

		for _, f := range g.functions {
			n := strings.ToLower(f.name)
			if strings.Contains(text, n) || strings.Contains(text, strings.ReplaceAll(n, "_", " ")) {
				return &actionCall{group: g, input: driver.InvocationInput{
					ActionGroup: g.name, Function: f.name, Parameters: extractParams(f.params, input),
				}}
			}
		}

		for j := range g.apis {
			api := &g.apis[j]

			p := strings.ToLower(strings.Trim(api.path, "/"))
			if p != "" && strings.Contains(text, p) {
				return &actionCall{group: g, input: apiInvocation(g.name, api, input)}
			}
		}
	}

	return nil
}

// apiInvocation builds the call of an API operation: its parameters, and its
// request-body properties when it takes a body, read from the input.
func apiInvocation(group string, api *apiDef, input string) driver.InvocationInput {
	call := driver.InvocationInput{
		ActionGroup: group, APIPath: api.path, HTTPMethod: api.method, Parameters: extractParams(api.params, input),
	}

	if api.bodyType != "" {
		call.RequestBody = &driver.RequestBody{ContentType: api.bodyType, Properties: extractParams(api.body, input)}
	}

	return call
}

// extractParams reads declared parameters written as name=value or
// name: value in the input. Missing parameters are left out.
func extractParams(params []paramDef, input string) []driver.Parameter {
	var out []driver.Parameter

	for _, p := range params {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(p.name) + `\s*[=:]\s*([^\s,;]+)`)
		if m := re.FindStringSubmatch(input); m != nil {
			typ := p.typ
			if typ == "" {
				typ = defaultParamType
			}

			out = append(out, driver.Parameter{Name: p.name, Type: typ, Value: m[1]})
		}
	}

	return out
}

// lambdaEvent is the event Bedrock Agents sends to an action-group Lambda.
type lambdaEvent struct {
	MessageVersion          string             `json:"messageVersion"`
	Agent                   lambdaAgent        `json:"agent"`
	InputText               string             `json:"inputText"`
	SessionID               string             `json:"sessionId"`
	ActionGroup             string             `json:"actionGroup"`
	Function                string             `json:"function,omitempty"`
	APIPath                 string             `json:"apiPath,omitempty"`
	HTTPMethod              string             `json:"httpMethod,omitempty"`
	Parameters              []lambdaParameter  `json:"parameters"`
	RequestBody             *lambdaRequestBody `json:"requestBody,omitempty"`
	SessionAttributes       map[string]string  `json:"sessionAttributes"`
	PromptSessionAttributes map[string]string  `json:"promptSessionAttributes"`
}

// lambdaRequestBody is an API call's body in the agent event:
// requestBody.content.<content type>.properties.
type lambdaRequestBody struct {
	Content map[string]struct {
		Properties []lambdaParameter `json:"properties"`
	} `json:"content"`
}

func toLambdaRequestBody(b *driver.RequestBody) *lambdaRequestBody {
	if b == nil {
		return nil
	}

	out := &lambdaRequestBody{Content: map[string]struct {
		Properties []lambdaParameter `json:"properties"`
	}{}}
	out.Content[b.ContentType] = struct {
		Properties []lambdaParameter `json:"properties"`
	}{Properties: lambdaParameters(b.Properties)}

	return out
}

// lambdaParameter is one parameter of the agent event, in the event's
// lowercase field names.
type lambdaParameter struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type lambdaAgent struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Alias   string `json:"alias"`
	Version string `json:"version"`
}

// Lambda response states (functionResponse.responseState).
const (
	responseFailure  = "FAILURE"
	responseReprompt = "REPROMPT"
)

// lambdaResponse is the Lambda's reply as the agent reads it: a function or
// API response body by content type, the function's responseState, and the
// session attributes and knowledge-base settings the function hands back.
type lambdaResponse struct {
	Response struct {
		FunctionResponse struct {
			ResponseState string `json:"responseState"`
			ResponseBody  map[string]struct {
				Body string `json:"body"`
			} `json:"responseBody"`
		} `json:"functionResponse"`
		ResponseBody map[string]struct {
			Body string `json:"body"`
		} `json:"responseBody"`
	} `json:"response"`
	SessionAttributes           map[string]string `json:"sessionAttributes"`
	PromptSessionAttributes     map[string]string `json:"promptSessionAttributes"`
	KnowledgeBasesConfiguration []struct {
		KnowledgeBaseID        string `json:"knowledgeBaseId"`
		RetrievalConfiguration *struct {
			VectorSearchConfiguration *struct {
				NumberOfResults *int32          `json:"numberOfResults"`
				Filter          json.RawMessage `json:"filter"`
			} `json:"vectorSearchConfiguration"`
		} `json:"retrievalConfiguration"`
	} `json:"knowledgeBasesConfiguration"`
}

// invokeActionLambda runs a Lambda-backed action group and returns the body of
// its response. A missing or failing function is a DependencyFailedException.
func (m *Mock) invokeActionLambda(ctx context.Context, ts *turnState, call *actionCall) (string, error) {
	if m.lambda == nil || call.group.lambdaARN == "" {
		return "", dependencyFailed(call.group.name, "action group %s has no Lambda function the emulator can run", call.group.name)
	}

	event := lambdaEvent{
		MessageVersion: lambdaMessageVersion,
		Agent:          lambdaAgent{Name: ts.cfg.name, ID: ts.cfg.agentID, Alias: ts.cfg.aliasID, Version: ts.cfg.version},
		InputText:      ts.req.inputText, SessionID: ts.req.sessionID, ActionGroup: call.input.ActionGroup,
		Function: call.input.Function, APIPath: call.input.APIPath, HTTPMethod: call.input.HTTPMethod,
		Parameters: lambdaParameters(call.input.Parameters), RequestBody: toLambdaRequestBody(call.input.RequestBody),
		SessionAttributes: nonNil(ts.conv.SessionAttributes), PromptSessionAttributes: nonNil(ts.conv.PromptSessionAttributes),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return "", err
	}

	name, qualifier := lambdaFunction(call.group.lambdaARN)

	out, err := m.lambda.Invoke(ctx, serverlessdriver.InvokeInput{FunctionName: name, Qualifier: qualifier, Payload: payload})
	if err != nil {
		return "", dependencyFailed(call.group.lambdaARN, "Lambda function %s failed: %s", call.group.lambdaARN, errors.Message(err))
	}

	if out.Error != "" {
		return "", dependencyFailed(call.group.lambdaARN, "Lambda function %s returned an error: %s", call.group.lambdaARN, out.Error)
	}

	return applyLambdaResponse(ts, call.group.lambdaARN, out.Payload)
}

// applyLambdaResponse reads the Lambda's reply: a FAILURE responseState is a
// DependencyFailedException; returned session and prompt-session attributes
// replace the conversation's, and knowledgeBasesConfiguration applies to the
// rest of the turn. REPROMPT is read as the observation, like a success (the
// emulator has no model to reprompt). It returns the response body.
func applyLambdaResponse(ts *turnState, lambdaARN string, payload []byte) (string, error) {
	var r lambdaResponse
	if json.Unmarshal(payload, &r) != nil {
		return string(payload), nil
	}

	if r.Response.FunctionResponse.ResponseState == responseFailure {
		return "", dependencyFailed(lambdaARN, "Lambda function %s responded with responseState FAILURE: %s",
			lambdaARN, lambdaBody(payload))
	}

	if r.SessionAttributes != nil {
		ts.conv.SessionAttributes = maps.Clone(r.SessionAttributes)
	}

	if r.PromptSessionAttributes != nil {
		ts.conv.PromptSessionAttributes = maps.Clone(r.PromptSessionAttributes)
	}

	if len(r.KnowledgeBasesConfiguration) > 0 {
		st := &driver.SessionState{}

		for _, kb := range r.KnowledgeBasesConfiguration {
			c := driver.KnowledgeBaseConfig{KnowledgeBaseID: kb.KnowledgeBaseID}
			if rc := kb.RetrievalConfiguration; rc != nil && rc.VectorSearchConfiguration != nil {
				c.NumberOfResults, c.Filter = rc.VectorSearchConfiguration.NumberOfResults, rc.VectorSearchConfiguration.Filter
			}

			st.KnowledgeBases = append(st.KnowledgeBases, c)
		}

		if err := applyKnowledgeBaseConfigs(ts.cfg, st); err != nil {
			return "", dependencyFailed(lambdaARN, "Lambda function %s returned a knowledgeBasesConfiguration the agent can't use: %s",
				lambdaARN, errors.Message(err))
		}
	}

	return lambdaBody(payload), nil
}

// lambdaBody returns the first response body of a Lambda reply, or the raw
// payload when it is not in the agent response format.
func lambdaBody(payload []byte) string {
	var r lambdaResponse
	if json.Unmarshal(payload, &r) == nil {
		for _, ct := range sortedKeys(r.Response.FunctionResponse.ResponseBody) {
			return r.Response.FunctionResponse.ResponseBody[ct].Body
		}

		for _, ct := range sortedKeys(r.Response.ResponseBody) {
			return r.Response.ResponseBody[ct].Body
		}
	}

	return string(payload)
}

// lambdaFunction splits a Lambda function ARN (or name) into the function name
// and an optional version or alias qualifier.
func lambdaFunction(arn string) (name, qualifier string) {
	_, rest, ok := strings.Cut(arn, ":function:")
	if !ok {
		return arn, ""
	}

	name, qualifier, _ = strings.Cut(rest, ":")

	return name, qualifier
}

func lambdaParameters(params []driver.Parameter) []lambdaParameter {
	out := make([]lambdaParameter, 0, len(params))
	for _, p := range params {
		out = append(out, lambdaParameter(p))
	}

	return out
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}

// dependencyFailed reports a failure of a resource the agent depends on (a
// Lambda function); the wire layer answers it with DependencyFailedException
// naming the resource.
func dependencyFailed(resource, format string, args ...any) error {
	return &driver.ResourceError{Err: errors.Newf(errors.Unavailable, format, args...), ResourceName: resource}
}

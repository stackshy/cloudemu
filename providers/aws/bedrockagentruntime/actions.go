package bedrockagentruntime

import (
	"context"
	"encoding/json"
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

		for _, api := range g.apis {
			p := strings.ToLower(strings.Trim(api.path, "/"))
			if p != "" && strings.Contains(text, p) {
				return &actionCall{group: g, input: driver.InvocationInput{
					ActionGroup: g.name, APIPath: api.path, HTTPMethod: api.method,
				}}
			}
		}
	}

	return nil
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
				typ = "string"
			}

			out = append(out, driver.Parameter{Name: p.name, Type: typ, Value: m[1]})
		}
	}

	return out
}

// lambdaEvent is the event Bedrock Agents sends to an action-group Lambda.
type lambdaEvent struct {
	MessageVersion          string            `json:"messageVersion"`
	Agent                   lambdaAgent       `json:"agent"`
	InputText               string            `json:"inputText"`
	SessionID               string            `json:"sessionId"`
	ActionGroup             string            `json:"actionGroup"`
	Function                string            `json:"function,omitempty"`
	APIPath                 string            `json:"apiPath,omitempty"`
	HTTPMethod              string            `json:"httpMethod,omitempty"`
	Parameters              []lambdaParameter `json:"parameters"`
	SessionAttributes       map[string]string `json:"sessionAttributes"`
	PromptSessionAttributes map[string]string `json:"promptSessionAttributes"`
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

// lambdaResponse is the part of the Lambda's reply the agent reads: a function
// response body or an API response body, by content type.
type lambdaResponse struct {
	Response struct {
		FunctionResponse struct {
			ResponseBody map[string]struct {
				Body string `json:"body"`
			} `json:"responseBody"`
		} `json:"functionResponse"`
		ResponseBody map[string]struct {
			Body string `json:"body"`
		} `json:"responseBody"`
	} `json:"response"`
}

// invokeActionLambda runs a Lambda-backed action group and returns the body of
// its response. A missing or failing function is a DependencyFailedException.
func (m *Mock) invokeActionLambda(ctx context.Context, ts *turnState, call *actionCall) (string, error) {
	if m.lambda == nil || call.group.lambdaARN == "" {
		return "", dependencyFailed("action group %s has no Lambda function the emulator can run", call.group.name)
	}

	event := lambdaEvent{
		MessageVersion: lambdaMessageVersion,
		Agent:          lambdaAgent{Name: ts.cfg.name, ID: ts.cfg.agentID, Alias: ts.cfg.aliasID, Version: ts.cfg.version},
		InputText:      ts.req.inputText, SessionID: ts.req.sessionID, ActionGroup: call.input.ActionGroup,
		Function: call.input.Function, APIPath: call.input.APIPath, HTTPMethod: call.input.HTTPMethod,
		Parameters:        lambdaParameters(call.input.Parameters),
		SessionAttributes: nonNil(ts.conv.SessionAttributes), PromptSessionAttributes: nonNil(ts.conv.PromptSessionAttributes),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return "", err
	}

	name, qualifier := lambdaFunction(call.group.lambdaARN)

	out, err := m.lambda.Invoke(ctx, serverlessdriver.InvokeInput{FunctionName: name, Qualifier: qualifier, Payload: payload})
	if err != nil {
		return "", dependencyFailed("Lambda function %s failed: %s", call.group.lambdaARN, errors.Message(err))
	}

	if out.Error != "" {
		return "", dependencyFailed("Lambda function %s returned an error: %s", call.group.lambdaARN, out.Error)
	}

	return lambdaBody(out.Payload), nil
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
// Lambda function); the wire layer answers it with DependencyFailedException.
func dependencyFailed(format string, args ...any) error {
	return errors.Newf(errors.Unavailable, format, args...)
}

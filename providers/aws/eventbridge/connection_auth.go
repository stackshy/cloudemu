package eventbridge

import (
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	authTypeBasic  = "BASIC"
	authTypeOAuth  = "OAUTH_CLIENT_CREDENTIALS"
	authTypeAPIKey = "API_KEY"

	maxConnectionNameLen = 64
	maxAuthValueLen      = 512
	maxHTTPParameters    = 100
	maxEndpointLen       = 2048
)

func authorizationTypes() []string {
	return []string{authTypeBasic, authTypeOAuth, authTypeAPIKey}
}

func oauthHTTPMethods() []string {
	return []string{httpGET, httpPOST, httpPUT}
}

// msgAuthParametersNull is AWS's reply to a CreateConnection without
// AuthParameters.
const msgAuthParametersNull = "1 validation error detected: Value null at 'authParameters' failed to satisfy constraint: " +
	"Member must not be null"

func validateConnectionConfig(cfg *driver.ConnectionConfig) error {
	var v violations

	v.name(cfg.Name, "name", maxConnectionNameLen)
	v.maxLen(cfg.Description, "description", maxDescriptionLen)
	v.enum(cfg.AuthorizationType, "authorizationType", authorizationTypes())

	if err := v.err(); err != nil {
		return err
	}

	if cfg.AuthParameters == nil {
		return errors.New(errors.InvalidArgument, msgAuthParametersNull)
	}

	return validateAuthParameters(cfg.AuthorizationType, cfg.AuthParameters)
}

// authBlockName is the AuthParameters member that carries the credentials of an
// authorization type.
func authBlockName(authType string) string {
	switch authType {
	case authTypeBasic:
		return "BasicAuthParameters"
	case authTypeAPIKey:
		return "ApiKeyAuthParameters"
	default:
		return "OAuthParameters"
	}
}

// validateAuthParameters checks that ap carries the credentials block for
// authType and no other, and that every field in it is within AWS's limits.
func validateAuthParameters(authType string, ap *driver.ConnectionAuthParameters) error {
	present := map[string]bool{
		authTypeBasic: ap.Basic != nil, authTypeAPIKey: ap.APIKey != nil, authTypeOAuth: ap.OAuth != nil,
	}

	if !present[authType] {
		return errors.Newf(errors.InvalidArgument,
			"%s must be specified for %s authorization type.", authBlockName(authType), authType)
	}

	for t, set := range present {
		if set && t != authType {
			return errors.Newf(errors.InvalidArgument,
				"Only the authorization parameters for the %s authorization type can be specified.", authType)
		}
	}

	var v violations

	authFieldViolations(&v, ap)
	httpParamViolations(&v, ap.InvocationHTTPParameters, "authParameters.invocationHttpParameters")

	return v.err()
}

func authFieldViolations(v *violations, ap *driver.ConnectionAuthParameters) {
	if b := ap.Basic; b != nil {
		v.required(b.Username, "authParameters.basicAuthParameters.username", maxAuthValueLen)
		v.requiredSensitive(b.Password, "authParameters.basicAuthParameters.password", maxAuthValueLen)
	}

	if k := ap.APIKey; k != nil {
		v.required(k.APIKeyName, "authParameters.apiKeyAuthParameters.apiKeyName", maxAuthValueLen)
		v.requiredSensitive(k.APIKeyValue, "authParameters.apiKeyAuthParameters.apiKeyValue", maxAuthValueLen)
	}

	if o := ap.OAuth; o != nil {
		v.required(o.AuthorizationEndpoint, "authParameters.oAuthParameters.authorizationEndpoint", maxEndpointLen)
		v.enum(o.HTTPMethod, "authParameters.oAuthParameters.httpMethod", oauthHTTPMethods())

		if o.ClientParameters == nil {
			v.add("null", "authParameters.oAuthParameters.clientParameters", "Member must not be null")
		} else {
			v.required(o.ClientParameters.ClientID, "authParameters.oAuthParameters.clientParameters.clientID", maxAuthValueLen)
			v.requiredSensitive(o.ClientParameters.ClientSecret, "authParameters.oAuthParameters.clientParameters.clientSecret",
				maxAuthValueLen)
		}

		httpParamViolations(v, o.OAuthHTTPParameters, "authParameters.oAuthParameters.oAuthHttpParameters")
	}
}

func httpParamViolations(v *violations, p *driver.ConnectionHTTPParameters, field string) {
	if p == nil {
		return
	}

	lists := []struct {
		name   string
		params []driver.HTTPParameter
		limit  bool // header and query string keys and values are capped at 512 bytes
	}{
		{"headerParameters", p.HeaderParameters, true},
		{"queryStringParameters", p.QueryStringParameters, true},
		{"bodyParameters", p.BodyParameters, false},
	}

	for _, l := range lists {
		if n := len(l.params); n > maxHTTPParameters {
			v.add(fmt.Sprint(n), field+"."+l.name, fmt.Sprintf("Member must have length less than or equal to %d", maxHTTPParameters))
		}

		if l.limit {
			httpParamValueViolations(v, l.params, field+"."+l.name)
		}
	}
}

// httpParamValueViolations checks each parameter's key and value length. The
// API model marks every parameter value sensitive, so a value is never echoed,
// whether or not IsValueSecret is set.
func httpParamValueViolations(v *violations, params []driver.HTTPParameter, field string) {
	for i := range params {
		member := fmt.Sprintf("%s.%d", field, i+1)

		if len(params[i].Key) > maxAuthValueLen {
			v.add(params[i].Key, member+".member.key", fmt.Sprintf("Member must have length less than or equal to %d", maxAuthValueLen))
		}

		v.maxLenSensitive(params[i].Value, member+".member.value", maxAuthValueLen)
	}
}

// mergeAuthParameters applies an UpdateConnection AuthParameters block onto the
// stored one: each credentials block given replaces the stored fields it sets,
// a block for another authorization type replaces the stored blocks, and
// InvocationHttpParameters replaces the stored set when given.
func mergeAuthParameters(old driver.ConnectionAuthParameters, upd *driver.ConnectionAuthParameters) driver.ConnectionAuthParameters {
	out := cloneAuth(old)
	if upd == nil {
		return out
	}

	if upd.Basic != nil {
		out.Basic = mergeBasic(out.Basic, upd.Basic)
	}

	if upd.APIKey != nil {
		out.APIKey = mergeAPIKey(out.APIKey, upd.APIKey)
	}

	if upd.OAuth != nil {
		out.OAuth = mergeOAuth(out.OAuth, upd.OAuth)
	}

	if upd.InvocationHTTPParameters != nil {
		out.InvocationHTTPParameters = cloneHTTPParams(upd.InvocationHTTPParameters)
	}

	return out
}

func mergeBasic(old, upd *driver.ConnectionBasicAuth) *driver.ConnectionBasicAuth {
	out := driver.ConnectionBasicAuth{}
	if old != nil {
		out = *old
	}

	out.Username = orKeep(upd.Username, out.Username)
	out.Password = orKeep(upd.Password, out.Password)

	return &out
}

func mergeAPIKey(old, upd *driver.ConnectionAPIKeyAuth) *driver.ConnectionAPIKeyAuth {
	out := driver.ConnectionAPIKeyAuth{}
	if old != nil {
		out = *old
	}

	out.APIKeyName = orKeep(upd.APIKeyName, out.APIKeyName)
	out.APIKeyValue = orKeep(upd.APIKeyValue, out.APIKeyValue)

	return &out
}

func mergeOAuth(old, upd *driver.ConnectionOAuth) *driver.ConnectionOAuth {
	out := driver.ConnectionOAuth{}
	if old != nil {
		out = *cloneOAuth(old)
	}

	out.AuthorizationEndpoint = orKeep(upd.AuthorizationEndpoint, out.AuthorizationEndpoint)
	out.HTTPMethod = orKeep(upd.HTTPMethod, out.HTTPMethod)

	if c := upd.ClientParameters; c != nil {
		client := driver.ConnectionOAuthClient{}
		if out.ClientParameters != nil {
			client = *out.ClientParameters
		}

		client.ClientID = orKeep(c.ClientID, client.ClientID)
		client.ClientSecret = orKeep(c.ClientSecret, client.ClientSecret)
		out.ClientParameters = &client
	}

	if upd.OAuthHTTPParameters != nil {
		out.OAuthHTTPParameters = cloneHTTPParams(upd.OAuthHTTPParameters)
	}

	return &out
}

func orKeep(v, keep string) string {
	if v == "" {
		return keep
	}

	return v
}

// onlyBlockFor keeps the credentials block of authType and drops the others, so
// a connection switched to another authorization type stops carrying the old
// credentials.
func onlyBlockFor(authType string, ap driver.ConnectionAuthParameters) driver.ConnectionAuthParameters {
	out := driver.ConnectionAuthParameters{InvocationHTTPParameters: ap.InvocationHTTPParameters}

	switch authType {
	case authTypeBasic:
		out.Basic = ap.Basic
	case authTypeAPIKey:
		out.APIKey = ap.APIKey
	default:
		out.OAuth = ap.OAuth
	}

	return out
}

func cloneAuth(ap driver.ConnectionAuthParameters) driver.ConnectionAuthParameters {
	out := driver.ConnectionAuthParameters{InvocationHTTPParameters: cloneHTTPParams(ap.InvocationHTTPParameters)}

	if ap.Basic != nil {
		b := *ap.Basic
		out.Basic = &b
	}

	if ap.APIKey != nil {
		k := *ap.APIKey
		out.APIKey = &k
	}

	if ap.OAuth != nil {
		out.OAuth = cloneOAuth(ap.OAuth)
	}

	return out
}

func cloneOAuth(o *driver.ConnectionOAuth) *driver.ConnectionOAuth {
	out := *o
	out.OAuthHTTPParameters = cloneHTTPParams(o.OAuthHTTPParameters)

	if o.ClientParameters != nil {
		c := *o.ClientParameters
		out.ClientParameters = &c
	}

	return &out
}

func cloneHTTPParams(p *driver.ConnectionHTTPParameters) *driver.ConnectionHTTPParameters {
	if p == nil {
		return nil
	}

	return &driver.ConnectionHTTPParameters{
		HeaderParameters:      append([]driver.HTTPParameter(nil), p.HeaderParameters...),
		QueryStringParameters: append([]driver.HTTPParameter(nil), p.QueryStringParameters...),
		BodyParameters:        append([]driver.HTTPParameter(nil), p.BodyParameters...),
	}
}

// redactAuth returns a copy of ap without any secret value: passwords, API key
// values, OAuth client secrets and HTTP parameters marked IsValueSecret. That is
// what DescribeConnection shows.
func redactAuth(ap driver.ConnectionAuthParameters) driver.ConnectionAuthParameters {
	out := cloneAuth(ap)

	if out.Basic != nil {
		out.Basic.Password = ""
	}

	if out.APIKey != nil {
		out.APIKey.APIKeyValue = ""
	}

	if out.OAuth != nil {
		if out.OAuth.ClientParameters != nil {
			out.OAuth.ClientParameters.ClientSecret = ""
		}

		redactHTTPParams(out.OAuth.OAuthHTTPParameters)
	}

	redactHTTPParams(out.InvocationHTTPParameters)

	return out
}

func redactHTTPParams(p *driver.ConnectionHTTPParameters) {
	if p == nil {
		return
	}

	for _, list := range [][]driver.HTTPParameter{p.HeaderParameters, p.QueryStringParameters, p.BodyParameters} {
		for i := range list {
			if list[i].IsValueSecret {
				list[i].Value = ""
			}
		}
	}
}

type secretHTTPParam struct {
	Key           string `json:"key"`
	Value         string `json:"value"`
	IsValueSecret bool   `json:"is_value_secret"`
}

type secretHTTPParams struct {
	HeaderParameters      []secretHTTPParam `json:"header_parameters,omitempty"`
	QueryStringParameters []secretHTTPParam `json:"query_string_parameters,omitempty"`
	BodyParameters        []secretHTTPParam `json:"body_parameters,omitempty"`
}

func toSecretParams(list []driver.HTTPParameter) []secretHTTPParam {
	out := make([]secretHTTPParam, 0, len(list))
	for _, p := range list {
		out = append(out, secretHTTPParam{Key: p.Key, Value: p.Value, IsValueSecret: p.IsValueSecret})
	}

	return out
}

// connectionSecretString renders the SecretString EventBridge stores in a
// connection's Secrets Manager secret: the credentials of the authorization
// type, plus the invocation HTTP parameters when the connection has any.
func connectionSecretString(ap *driver.ConnectionAuthParameters) []byte {
	fields := map[string]any{}

	if b := ap.Basic; b != nil {
		fields["username"], fields["password"] = b.Username, b.Password
	}

	if k := ap.APIKey; k != nil {
		fields["api_key_name"], fields["api_key_value"] = k.APIKeyName, k.APIKeyValue
	}

	if o := ap.OAuth; o != nil {
		fields["authorization_endpoint"], fields["http_method"] = o.AuthorizationEndpoint, o.HTTPMethod

		if c := o.ClientParameters; c != nil {
			fields["client_id"], fields["client_secret"] = c.ClientID, c.ClientSecret
		}
	}

	if p := ap.InvocationHTTPParameters; p != nil {
		fields["invocation_http_parameters"] = secretHTTPParams{
			HeaderParameters:      toSecretParams(p.HeaderParameters),
			QueryStringParameters: toSecretParams(p.QueryStringParameters),
			BodyParameters:        toSecretParams(p.BodyParameters),
		}
	}

	out, err := json.Marshal(fields)
	if err != nil {
		return []byte("{}")
	}

	return out
}

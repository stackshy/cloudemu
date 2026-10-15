package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultParamType is the type of a parameter whose schema names none.
const defaultParamType = "string"

// httpMethods are the OpenAPI path-item keys that name an operation.
//
//nolint:gochecknoglobals // fixed lookup table.
var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}

// apiSchemaJSON is an action group's apiSchema union: an inline payload or an
// S3 object.
type apiSchemaJSON struct {
	Payload string `json:"payload"`
	S3      *struct {
		Bucket string `json:"s3BucketName"`
		Key    string `json:"s3ObjectKey"`
	} `json:"s3"`
}

// openAPIOperations lists the operations of an action group's OpenAPI schema,
// inline (JSON or YAML) or in cloudemu S3. A schema that can't be read or
// parsed has no operations.
func (m *Mock) openAPIOperations(ctx context.Context, raw json.RawMessage) []apiDef {
	var schema apiSchemaJSON
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil {
		return nil
	}

	text := schema.Payload
	if text == "" && schema.S3 != nil {
		read, err := m.readS3Text(ctx, s3Scheme+schema.S3.Bucket+"/"+schema.S3.Key)
		if err != nil {
			return nil
		}

		text = read
	}

	return parseOpenAPI(text)
}

// parseOpenAPI lists the operations of an OpenAPI document in JSON or YAML
// (YAML reads JSON too), with each operation's parameters and the properties
// of its request body.
func parseOpenAPI(text string) []apiDef {
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}

	if text == "" || yaml.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}

	var out []apiDef

	for _, path := range sortedKeys(doc.Paths) {
		for _, method := range sortedKeys(doc.Paths[path]) {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}

			op, _ := doc.Paths[path][method].(map[string]any)
			params := mergeParams(operationParams(doc.Paths[path]), operationParams(op))
			api := apiDef{path: path, method: strings.ToUpper(method), params: params}
			api.bodyType, api.body = requestBodyProperties(op)
			out = append(out, api)
		}
	}

	return out
}

// mergeParams adds a path item's parameters to an operation's, as OpenAPI
// does: an operation parameter with the same name and location overrides the
// path item's.
func mergeParams(pathLevel, operation []paramDef) []paramDef {
	out := append([]paramDef{}, operation...)

	for _, p := range pathLevel {
		overridden := false

		for _, o := range operation {
			if o.name == p.name && o.in == p.in {
				overridden = true

				break
			}
		}

		if !overridden {
			out = append(out, p)
		}
	}

	return out
}

// operationParams reads the parameters of an operation or a path item (path,
// query and header).
func operationParams(op map[string]any) []paramDef {
	list, _ := op["parameters"].([]any)
	out := make([]paramDef, 0, len(list))

	for _, item := range list {
		p, _ := item.(map[string]any)

		name, _ := p["name"].(string)
		if name == "" {
			continue
		}

		required, _ := p["required"].(bool)
		in, _ := p["in"].(string)
		out = append(out, paramDef{name: name, typ: schemaType(p["schema"]), required: required, in: in})
	}

	return out
}

// requestBodyProperties reads the first content type of an operation's
// request body and its schema's properties.
func requestBodyProperties(op map[string]any) (contentType string, props []paramDef) {
	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)

	types := sortedKeys(content)
	if len(types) == 0 {
		return "", nil
	}

	contentType = types[0]
	media, _ := content[contentType].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	required := map[string]bool{}

	list, _ := schema["required"].([]any)
	for _, r := range list {
		if name, ok := r.(string); ok {
			required[name] = true
		}
	}

	for _, name := range sortedKeys(properties) {
		props = append(props, paramDef{name: name, typ: schemaType(properties[name]), required: required[name]})
	}

	return contentType, props
}

// schemaType returns a JSON schema's type, "string" when it names none.
func schemaType(schema any) string {
	s, _ := schema.(map[string]any)
	if t, ok := s["type"].(string); ok && t != "" {
		return t
	}

	return defaultParamType
}

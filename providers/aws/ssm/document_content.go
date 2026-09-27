package ssm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/yamlconv"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

// maxDocumentBytes is the 64 KB document size limit.
const maxDocumentBytes = 64 * 1024

// Platform types as the SSM API spells them, in the order it lists them.
const (
	platformWindows = "Windows"
	platformLinux   = "Linux"
	platformMacOS   = "MacOS"
)

// contentMeta is what SSM derives from a document's content.
type contentMeta struct {
	schemaVersion string
	description   string
	parameters    []ssmdriver.DocumentParameter
	platformTypes []string
	hash          string
}

// supportedSchemas lists the schemaVersion values each document type accepts.
// Types missing from the map do not require a schemaVersion.
func supportedSchemas(docType string) []string {
	switch docType {
	case ssmdriver.DocumentTypeCommand:
		return []string{"1.2", "2.0", "2.2"}
	case ssmdriver.DocumentTypeAutomation, "Automation.ChangeTemplate":
		return []string{"0.3"}
	case ssmdriver.DocumentTypeSession:
		return []string{"1.0"}
	case ssmdriver.DocumentTypePolicy:
		return []string{"2.0", "2.2"}
	case ssmdriver.DocumentTypePackage:
		return []string{"2.0"}
	default:
		return nil
	}
}

// parseContent validates content for its format and type and derives the
// metadata SSM reports for it.
func parseContent(content, format, docType string) (*contentMeta, error) {
	if len(content) > maxDocumentBytes {
		return nil, ssmErrf(excMaxDocumentSizeExceeded, errors.InvalidArgument, "The size limit of a document is 64 KB.")
	}

	sum := sha256.Sum256([]byte(content))
	meta := &contentMeta{hash: hex.EncodeToString(sum[:])}

	if format == ssmdriver.FormatText {
		return meta, nil
	}

	root, err := decodeContent(content, format)
	if err != nil {
		return nil, err
	}

	if err := checkSchema(root, docType, meta); err != nil {
		return nil, err
	}

	meta.description, _ = root["description"].(string)
	meta.parameters = documentParameters(root["parameters"])
	meta.platformTypes = platformTypes(root, docType)

	return meta, nil
}

// decodeContent parses JSON or YAML content into a top-level object.
func decodeContent(content, format string) (map[string]any, error) {
	var (
		tree any
		err  error
	)

	if format == ssmdriver.FormatYAML {
		tree, err = yamlconv.Decode([]byte(content), nil)
		if err != nil {
			return nil, ssmErrf(excInvalidDocumentContent, errors.InvalidArgument, "YAML not well-formed. %v", err)
		}
	} else {
		dec := json.NewDecoder(strings.NewReader(content))
		dec.UseNumber()

		if err = dec.Decode(&tree); err != nil {
			return nil, ssmErrf(excInvalidDocumentContent, errors.InvalidArgument, "JSON not well-formed. %v", err)
		}
	}

	root, ok := tree.(map[string]any)
	if !ok {
		return nil, ssmErrf(excInvalidDocumentContent, errors.InvalidArgument, "Document content must be an object.")
	}

	return root, nil
}

// checkSchema checks schemaVersion and the steps section its schema needs.
func checkSchema(root map[string]any, docType string, meta *contentMeta) error {
	meta.schemaVersion = scalarString(root["schemaVersion"])
	allowed := supportedSchemas(docType)

	if allowed == nil {
		return nil
	}

	if meta.schemaVersion == "" {
		return ssmErrf(excInvalidDocumentContent, errors.InvalidArgument, "schemaVersion is missing from the document.")
	}

	if !slices.Contains(allowed, meta.schemaVersion) {
		return ssmErrf(excInvalidDocumentSchemaVersion, errors.InvalidArgument,
			"Document schema version, %s, is not supported by association that is created with this document.",
			meta.schemaVersion)
	}

	section := "mainSteps"
	if meta.schemaVersion == "1.2" {
		section = "runtimeConfig"
	}

	switch docType {
	case ssmdriver.DocumentTypeSession, ssmdriver.DocumentTypePolicy, ssmdriver.DocumentTypePackage:
		return nil
	}

	if !nonEmpty(root[section]) {
		return ssmErrf(excInvalidDocumentContent, errors.InvalidArgument,
			"%s is required for a %s document with schemaVersion %s.", section, docType, meta.schemaVersion)
	}

	return nil
}

func nonEmpty(v any) bool {
	switch t := v.(type) {
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return false
	}
}

// scalarString renders a string or number (YAML allows an unquoted 2.2).
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return fmt.Sprint(t)
	default:
		return ""
	}
}

// documentParameters lists the declared input parameters sorted by name.
// A non-string default is rendered as JSON.
func documentParameters(v any) []ssmdriver.DocumentParameter {
	params, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	out := make([]ssmdriver.DocumentParameter, 0, len(params))

	for name, raw := range params {
		spec, _ := raw.(map[string]any)
		p := ssmdriver.DocumentParameter{Name: name, Type: scalarString(spec["type"])}
		p.Description, _ = spec["description"].(string)

		if def, ok := spec["default"]; ok {
			p.DefaultValue = scalarString(def)
			if p.DefaultValue == "" && def != nil {
				b, _ := json.Marshal(def)
				p.DefaultValue = string(b)
			}
		}

		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// pluginPlatforms maps Command plugins to the platforms they run on. A plugin
// not listed runs everywhere.
func pluginPlatforms(plugin string) []string {
	switch plugin {
	case "aws:runShellScript":
		return []string{platformLinux, platformMacOS}
	case "aws:applications", "aws:domainJoin", "aws:psModule", "aws:cloudWatch":
		return []string{platformWindows}
	case "aws:configureDocker", "aws:runDockerAction":
		return []string{platformWindows, platformLinux}
	default:
		return []string{platformWindows, platformLinux, platformMacOS}
	}
}

// platformTypes derives PlatformTypes from the plugins a Command document
// uses. Other document types run on every platform.
func platformTypes(root map[string]any, docType string) []string {
	if docType != ssmdriver.DocumentTypeCommand {
		return []string{platformWindows, platformLinux, platformMacOS}
	}

	var plugins []string

	if rc, ok := root["runtimeConfig"].(map[string]any); ok {
		for name := range rc {
			plugins = append(plugins, name)
		}
	}

	steps, _ := root["mainSteps"].([]any)
	for _, s := range steps {
		if step, ok := s.(map[string]any); ok {
			plugins = append(plugins, scalarString(step["action"]))
		}
	}

	seen := map[string]bool{}

	for _, p := range plugins {
		for _, pt := range pluginPlatforms(p) {
			seen[pt] = true
		}
	}

	out := make([]string, 0, len(seen))

	for _, pt := range []string{platformWindows, platformLinux, platformMacOS} {
		if seen[pt] {
			out = append(out, pt)
		}
	}

	return out
}

// convertContent renders content stored in format from as format to. TEXT
// content and a matching format come back unchanged.
func convertContent(content, from, to string) (string, error) {
	if to == "" || to == from || from == ssmdriver.FormatText || to == ssmdriver.FormatText {
		return content, nil
	}

	if to == ssmdriver.FormatJSON {
		tree, err := yamlconv.Decode([]byte(content), nil)
		if err != nil {
			return "", errors.Newf(errors.Internal, "convert document to JSON: %v", err)
		}

		b, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return "", errors.Newf(errors.Internal, "convert document to JSON: %v", err)
		}

		return string(b), nil
	}

	// JSON is valid YAML, so parsing it as a node keeps the key order. Clearing
	// the flow style makes the encoder write block YAML.
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		return "", errors.Newf(errors.Internal, "convert document to YAML: %v", err)
	}

	blockStyle(&node)

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2) //nolint:mnd // two-space YAML indent

	if err := enc.Encode(&node); err != nil {
		return "", errors.Newf(errors.Internal, "convert document to YAML: %v", err)
	}

	return buf.String(), nil
}

func blockStyle(n *yaml.Node) {
	if n.Kind != yaml.ScalarNode {
		n.Style = 0
	} else if n.Style == yaml.DoubleQuotedStyle && n.Tag == "!!str" {
		n.Style = 0
	}

	for _, c := range n.Content {
		blockStyle(c)
	}
}

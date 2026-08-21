package kubernetes

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
)

// `oc process` server-side: POST a Template to
// /apis/template.openshift.io/v1/namespaces/<ns>/processedtemplates and get the
// Template back with every parameter resolved (provided value, else a value
// generated from its `generate`/`from` expression) and every ${PARAM} /
// ${{PARAM}} reference in the objects substituted. It does NOT create the
// objects — that is what a client then pipes into `oc apply`. This matches the
// real processedtemplates endpoint's contract.

// processedTemplateTarget returns the namespace a processedtemplates POST
// targets, or ok=false when the path is not that endpoint.
func processedTemplateTarget(path string) (namespace string, ok bool) {
	const marker = "/apis/template.openshift.io/v1/namespaces/"
	if !strings.HasPrefix(path, marker) || !strings.HasSuffix(path, "/processedtemplates") {
		return "", false
	}

	rest := strings.TrimSuffix(strings.TrimPrefix(path, marker), "/processedtemplates")
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}

	return rest, true
}

// serveProcessedTemplate resolves a Template's parameters and substitutes them
// into its objects, returning the processed Template.
func (*ClusterState) serveProcessedTemplate(w http.ResponseWriter, r *http.Request) {
	var tmpl map[string]any
	if err := json.NewDecoder(r.Body).Decode(&tmpl); err != nil {
		writeBadRequest(w, "openshift: processedtemplate: invalid body: "+err.Error())

		return
	}

	values := resolveTemplateParameters(tmpl)

	if objs, ok := tmpl["objects"].([]any); ok {
		substituted, err := substituteTemplateObjects(objs, values)
		if err != nil {
			writeBadRequest(w, "openshift: processedtemplate: "+err.Error())

			return
		}

		tmpl["objects"] = substituted
	}

	tmpl["apiVersion"] = apiGroupOSTemplate + "/v1"
	tmpl["kind"] = "Template"

	writeJSON(w, http.StatusOK, tmpl)
}

// resolveTemplateParameters fills each parameter's value in place (using the
// provided value, else generating one from its expression) and returns the
// name->value map used for substitution.
func resolveTemplateParameters(tmpl map[string]any) map[string]string {
	values := map[string]string{}

	params, ok := tmpl["parameters"].([]any)
	if !ok {
		return values
	}

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		name, _ := p["name"].(string)
		if name == "" {
			continue
		}

		value, _ := p["value"].(string)
		if value == "" {
			if from, _ := p["from"].(string); from != "" {
				if gen, _ := p["generate"].(string); gen != "" {
					value = genFromExpression(from)
				}
			}
		}

		p["value"] = value
		values[name] = value
	}

	return values
}

// substituteTemplateObjects replaces ${{PARAM}} (unquoted, for numeric/bool
// fields) then ${PARAM} (string) across each object's JSON. Two passes over the
// marshaled form keep it type-agnostic without walking the object tree.
func substituteTemplateObjects(objs []any, values map[string]string) ([]any, error) {
	out := make([]any, 0, len(objs))

	for _, obj := range objs {
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}

		text := string(b)
		for name, value := range values {
			text = strings.ReplaceAll(text, "${{"+name+"}}", value)
			text = strings.ReplaceAll(text, "${"+name+"}", value)
		}

		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, err
		}

		out = append(out, decoded)
	}

	return out, nil
}

// genFromExpression expands an OpenShift parameter `from` pseudo-regex — a
// sequence of literal characters and [class]{count} segments (e.g.
// "[a-z0-9]{16}") — into a random string. Unsupported constructs degrade to
// their literal characters.
func genFromExpression(from string) string {
	var b strings.Builder

	runes := []rune(from)
	for i := 0; i < len(runes); {
		if runes[i] != '[' {
			b.WriteRune(runes[i])

			i++

			continue
		}

		closeIdx := indexRune(runes, ']', i+1)
		if closeIdx < 0 {
			b.WriteRune(runes[i])

			i++

			continue
		}

		charset := expandCharClass(string(runes[i+1 : closeIdx]))

		count, next := parseCount(runes, closeIdx+1)
		if count == 0 {
			count = 1
		}

		b.WriteString(genRandomString(charset, count))

		i = next
	}

	return b.String()
}

// expandCharClass expands a character class body ("a-z0-9", "A-Za-z") into the
// concrete set of characters it matches.
func expandCharClass(body string) string {
	var b strings.Builder

	r := []rune(body)
	for i := 0; i < len(r); {
		if i+2 < len(r) && r[i+1] == '-' {
			for c := r[i]; c <= r[i+2]; c++ {
				b.WriteRune(c)
			}

			i += 3

			continue
		}

		b.WriteRune(r[i])

		i++
	}

	if b.Len() == 0 {
		return "abcdefghijklmnopqrstuvwxyz0123456789"
	}

	return b.String()
}

// parseCount reads a {n} quantifier starting at i, returning the count and the
// index past the '}'. Without a quantifier it returns (1, i).
func parseCount(runes []rune, i int) (count, next int) {
	if i >= len(runes) || runes[i] != '{' {
		return 1, i
	}

	closeIdx := indexRune(runes, '}', i+1)
	if closeIdx < 0 {
		return 1, i
	}

	n := 0

	for _, c := range runes[i+1 : closeIdx] {
		if c < '0' || c > '9' {
			return 1, i
		}

		n = n*10 + int(c-'0')
	}

	return n, closeIdx + 1
}

func indexRune(runes []rune, target rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}

	return -1
}

// genRandomString returns n characters drawn uniformly from charset.
func genRandomString(charset string, n int) string {
	if charset == "" || n <= 0 {
		return ""
	}

	buf := make([]byte, n)
	_, _ = rand.Read(buf)

	cs := []byte(charset)
	for i := range buf {
		buf[i] = cs[int(buf[i])%len(cs)]
	}

	return string(buf)
}

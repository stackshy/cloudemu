// Package awsquery provides parsers and encoders for the AWS query-protocol
// wire format used by EC2, Auto-Scaling, STS, and several other services.
//
// The query protocol is a POST (or GET) with form-encoded parameters where
// lists are flattened with dotted indices:
//
//	Action=RunInstances&ImageId=ami-123&InstanceType=t2.micro
//	  &SecurityGroupId.1=sg-a&SecurityGroupId.2=sg-b
//	  &TagSpecification.1.ResourceType=instance
//	  &TagSpecification.1.Tag.1.Key=Name
//	  &TagSpecification.1.Tag.1.Value=my-box
//	  &Filter.1.Name=instance-state-name&Filter.1.Value.1=running
//
// Responses are XML envelopes carrying a RequestId and a service-specific
// payload under a top-level <ActionResponse> element.
package awsquery

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Namespace is the XML namespace for AWS EC2 responses.
const Namespace = "http://ec2.amazonaws.com/doc/2016-11-15/"

// RequestID is the stub request id embedded in every response. Real AWS uses
// UUIDs; any well-formed value satisfies SDK clients.
const RequestID = "00000000-0000-0000-0000-000000000000"

// Filter is one entry parsed from a Filter.N parameter group.
type Filter struct {
	Name   string
	Values []string
}

// TagSpec is one entry parsed from a TagSpecification.N parameter group.
type TagSpec struct {
	ResourceType string
	Tags         map[string]string
}

// ListStrings collects values of a flattened string list from form parameters.
// For prefix "SecurityGroupId" it returns values for keys
// "SecurityGroupId.1", "SecurityGroupId.2", ... in index order.
func ListStrings(form url.Values, prefix string) []string {
	indexed := collectIndices(form, prefix)
	if len(indexed) == 0 {
		return nil
	}

	out := make([]string, 0, len(indexed))

	for _, idx := range indexed {
		key := prefix + "." + strconv.Itoa(idx)
		if v := form.Get(key); v != "" {
			out = append(out, v)
		}
	}

	return out
}

// Filters parses Filter.N.Name / Filter.N.Value.M groups into []Filter.
func Filters(form url.Values) []Filter {
	indexed := collectIndices(form, "Filter")
	if len(indexed) == 0 {
		return nil
	}

	out := make([]Filter, 0, len(indexed))

	for _, idx := range indexed {
		base := "Filter." + strconv.Itoa(idx)

		name := form.Get(base + ".Name")
		if name == "" {
			continue
		}

		values := ListStrings(form, base+".Value")
		out = append(out, Filter{Name: name, Values: values})
	}

	return out
}

// TagSpecs parses TagSpecification.N.ResourceType plus
// TagSpecification.N.Tag.M.Key / TagSpecification.N.Tag.M.Value groups.
func TagSpecs(form url.Values) []TagSpec {
	indexed := collectIndices(form, "TagSpecification")
	if len(indexed) == 0 {
		return nil
	}

	out := make([]TagSpec, 0, len(indexed))

	for _, idx := range indexed {
		base := "TagSpecification." + strconv.Itoa(idx)
		spec := TagSpec{
			ResourceType: form.Get(base + ".ResourceType"),
			Tags:         parseTagMap(form, base+".Tag"),
		}
		out = append(out, spec)
	}

	return out
}

// FlatTags parses the simpler Tag.N.Key / Tag.N.Value form used by APIs such
// as CreateTags on an existing resource. The prefix is typically "Tag".
func FlatTags(form url.Values, prefix string) map[string]string {
	return parseTagMap(form, prefix)
}

func parseTagMap(form url.Values, prefix string) map[string]string {
	indexed := collectIndices(form, prefix)
	if len(indexed) == 0 {
		return nil
	}

	out := make(map[string]string, len(indexed))

	for _, idx := range indexed {
		base := prefix + "." + strconv.Itoa(idx)

		k := form.Get(base + ".Key")
		if k == "" {
			continue
		}

		out[k] = form.Get(base + ".Value")
	}

	return out
}

// CollectIndices returns the unique ascending N values for which any form key
// starts with "<prefix>.N" or "<prefix>.N.*". Exposed so sibling packages
// parsing deeply-nested AWS wire structures can reuse the same logic.
func CollectIndices(form url.Values, prefix string) []int {
	return collectIndices(form, prefix)
}

func collectIndices(form url.Values, prefix string) []int {
	seen := make(map[int]struct{})
	dot := prefix + "."

	for key := range form {
		if !strings.HasPrefix(key, dot) {
			continue
		}

		rest := key[len(dot):]

		end := strings.Index(rest, ".")
		if end < 0 {
			end = len(rest)
		}

		n, err := strconv.Atoi(rest[:end])
		if err != nil {
			continue
		}

		seen[n] = struct{}{}
	}

	if len(seen) == 0 {
		return nil
	}

	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}

	sort.Ints(out)

	return out
}

// WriteXMLResponse marshals a pre-built response envelope to the client. The
// caller's struct is expected to carry an XMLName of "<Action>Response" and
// an xmlns attr (see examples in server/aws/ec2). This function only handles
// the HTTP preamble so every op has a uniform response path.
func WriteXMLResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, xml.Header)

	_ = xml.NewEncoder(w).Encode(v)
}

// ErrorResponse is the AWS-style XML error body. It emits BOTH the EC2 query
// shape (<Errors><Error>...</Error></Errors>) and the standard query shape
// (<Error>...</Error> at the response root) so that:
//
//   - EC2 SDK (which parses xml:"Errors>Error>Code") finds the wrapped form.
//   - RDS / Redshift / Neptune / DocumentDB SDKs (which parse xml:"Error>Code"
//     against a wrappedErrorResponse) find the unwrapped form.
//
// The duplication is harmless: each SDK matches its own shape via XPath-style
// xml tags and ignores the other.
type ErrorResponse struct {
	XMLName   xml.Name `xml:"Response"`
	Errors    []Error  `xml:"Errors>Error"`
	Error     Error    `xml:"Error"`
	RequestID string   `xml:"RequestID"`
	//nolint:revive,stylecheck,staticcheck // SDKs literally look for "RequestId", not "RequestID"; the second field is intentional.
	RequestId string `xml:"RequestId"`
}

// Error is one error entry.
type Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// WriteXMLError writes an AWS-style XML error response. The response shape is
// compatible with both the EC2 query SDK and the standard query SDKs (RDS,
// Redshift, Neptune, DocumentDB).
func WriteXMLError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, xml.Header)

	_ = xml.NewEncoder(w).Encode(ErrorResponse{
		Errors:    []Error{{Code: code, Message: message}},
		Error:     Error{Code: code, Message: message},
		RequestID: RequestID,
		RequestId: RequestID,
	})
}

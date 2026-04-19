package awsquery_test

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

func TestListStrings(t *testing.T) {
	form := url.Values{}
	form.Set("SecurityGroupId.1", "sg-aaa")
	form.Set("SecurityGroupId.2", "sg-bbb")
	form.Set("SecurityGroupId.3", "sg-ccc")
	form.Set("OtherField", "noise")

	got := awsquery.ListStrings(form, "SecurityGroupId")
	want := []string{"sg-aaa", "sg-bbb", "sg-ccc"}

	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListStringsPreservesOrder(t *testing.T) {
	form := url.Values{}
	form.Set("InstanceId.10", "i-j")
	form.Set("InstanceId.1", "i-a")
	form.Set("InstanceId.2", "i-b")

	got := awsquery.ListStrings(form, "InstanceId")
	want := []string{"i-a", "i-b", "i-j"}

	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListStringsEmpty(t *testing.T) {
	form := url.Values{}
	form.Set("Unrelated", "x")

	if got := awsquery.ListStrings(form, "SecurityGroupId"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestFilters(t *testing.T) {
	form := url.Values{}
	form.Set("Filter.1.Name", "instance-state-name")
	form.Set("Filter.1.Value.1", "running")
	form.Set("Filter.1.Value.2", "stopped")
	form.Set("Filter.2.Name", "tag:Name")
	form.Set("Filter.2.Value.1", "web")

	got := awsquery.Filters(form)
	if len(got) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(got))
	}

	if got[0].Name != "instance-state-name" ||
		!equalStrings(got[0].Values, []string{"running", "stopped"}) {
		t.Errorf("filter 0 wrong: %+v", got[0])
	}

	if got[1].Name != "tag:Name" || !equalStrings(got[1].Values, []string{"web"}) {
		t.Errorf("filter 1 wrong: %+v", got[1])
	}
}

func TestTagSpecs(t *testing.T) {
	form := url.Values{}
	form.Set("TagSpecification.1.ResourceType", "instance")
	form.Set("TagSpecification.1.Tag.1.Key", "Name")
	form.Set("TagSpecification.1.Tag.1.Value", "my-box")
	form.Set("TagSpecification.1.Tag.2.Key", "Env")
	form.Set("TagSpecification.1.Tag.2.Value", "dev")

	got := awsquery.TagSpecs(form)
	if len(got) != 1 {
		t.Fatalf("expected 1 tag spec, got %d", len(got))
	}

	if got[0].ResourceType != "instance" {
		t.Errorf("ResourceType = %q, want instance", got[0].ResourceType)
	}

	want := map[string]string{"Name": "my-box", "Env": "dev"}
	if !equalMaps(got[0].Tags, want) {
		t.Errorf("tags = %v, want %v", got[0].Tags, want)
	}
}

func TestFlatTags(t *testing.T) {
	form := url.Values{}
	form.Set("Tag.1.Key", "k1")
	form.Set("Tag.1.Value", "v1")
	form.Set("Tag.2.Key", "k2")
	form.Set("Tag.2.Value", "v2")

	got := awsquery.FlatTags(form, "Tag")

	want := map[string]string{"k1": "v1", "k2": "v2"}
	if !equalMaps(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCollectIndices(t *testing.T) {
	form := url.Values{
		"Foo.1":         {"a"},
		"Foo.2.Bar":     {"b"},
		"Foo.10":        {"j"},
		"Foo.3.Baz.1":   {"c"},
		"Other.1":       {"x"},
		"NotIndexed":    {"y"},
		"Foo.notanint":  {"z"},
	}

	got := awsquery.CollectIndices(form, "Foo")

	want := []int{1, 2, 3, 10}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCollectIndicesEmpty(t *testing.T) {
	if got := awsquery.CollectIndices(url.Values{"Other": {"x"}}, "Foo"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

type sampleResponse struct {
	XMLName xml.Name `xml:"SampleResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Value   string   `xml:"value"`
}

func TestWriteXMLResponse(t *testing.T) {
	rr := httptest.NewRecorder()

	awsquery.WriteXMLResponse(rr, sampleResponse{
		Xmlns: awsquery.Namespace,
		Value: "hello",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/xml" {
		t.Errorf("Content-Type = %q, want text/xml", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "<?xml") {
		t.Errorf("body should start with XML declaration: %s", body)
	}

	if !strings.Contains(body, "<SampleResponse") {
		t.Errorf("body should contain root element: %s", body)
	}

	if !strings.Contains(body, "<value>hello</value>") {
		t.Errorf("body should contain payload: %s", body)
	}
}

func TestWriteXMLError(t *testing.T) {
	rr := httptest.NewRecorder()

	awsquery.WriteXMLError(rr, http.StatusBadRequest, "InvalidParameter", "bad value")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/xml" {
		t.Errorf("Content-Type = %q, want text/xml", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "<Code>InvalidParameter</Code>") {
		t.Errorf("body should contain Code: %s", body)
	}

	if !strings.Contains(body, "<Message>bad value</Message>") {
		t.Errorf("body should contain Message: %s", body)
	}

	if !strings.Contains(body, "<RequestID>") {
		t.Errorf("body should contain RequestID: %s", body)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}

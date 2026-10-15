package apigateway_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// TestMockTemplateAbuseIsAnError covers templates that would otherwise recurse
// forever or grow without bound: each must end as a 500 from the gateway.
func TestMockTemplateAbuseIsAnError(t *testing.T) {
	for name, tmpl := range map[string]string{
		"self list":       `#set($l = [])#set($x = $l.add($l))$l`,
		"self list json":  `#set($l = [])#set($x = $l.add($l))$util.parseJson("[]")$input.json('$')`,
		"string doubling": `#set($s = "ab")#foreach($i in [1..200])#set($s = "$s$s")#end$s`,
		"list doubling":   `#set($l = [1])#foreach($i in [1..200])#set($x = $l.addAll($l))#end$l.size()`,
	} {
		t.Run(name, func(t *testing.T) {
			m := newMock(t)
			apiID, _ := mockMethod(t, m, map[string]string{"application/json": `{"statusCode": 200}`}, "", tmpl)

			resp := invokeMock(t, m, apiID, driver.ProxyRequest{})

			switch name {
			case "self list":
				if resp.StatusCode != 200 || resp.Body != "[(this Collection)]" {
					t.Fatalf("got %d %q", resp.StatusCode, resp.Body)
				}
			case "self list json":
				if resp.StatusCode != 200 || resp.Body != "[]{}" {
					t.Fatalf("got %d %q", resp.StatusCode, resp.Body)
				}
			default:
				if resp.StatusCode != 500 || resp.Headers["x-amzn-ErrorType"] != "InternalServerErrorException" {
					t.Fatalf("got %d %.80q", resp.StatusCode, resp.Body)
				}
			}
		})
	}
}

func TestMockUtilEncodingAndPaths(t *testing.T) {
	m := newMock(t)
	tmpl := `$util.urlEncode('a~b*c d')|$util.escapeJavaScript("x` + "\x7f" + `y")|` +
		`$input.json('$.items[*].id')|$input.json('$..id')|$input.path('$.items[0].id')`
	apiID, _ := mockMethod(t, m, map[string]string{"application/json": `{"statusCode": 200}`}, "", tmpl)

	// The MOCK backend body is empty, so paths select from the response
	// template's own (empty) input: the list forms render as empty lists.
	resp := invokeMock(t, m, apiID, driver.ProxyRequest{})

	want := "a%7Eb*c+d|x\x7fy|[]|[]|"
	if resp.StatusCode != 200 || resp.Body != want {
		t.Fatalf("got %d %q, want %q", resp.StatusCode, resp.Body, want)
	}
}

func TestMockRequestTemplatePaths(t *testing.T) {
	m := newMock(t)
	reqTmpl := `#set($ids = $input.path('$.items[*].id'))` +
		`{"statusCode": #if($ids.size() == 2 && $input.json('$..id') == "[1,2]")201#{else}500#end}`
	apiID, resID := mockMethod(t, m, map[string]string{"application/json": reqTmpl}, "", "")

	if _, err := m.PutMethodResponse(ctx(), apiID, resID, "GET", "201", driver.PutMethodResponseInput{}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PutIntegrationResponse(ctx(), apiID, resID, "GET", "201", driver.PutIntegrationResponseInput{
		SelectionPattern: "201",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateDeployment(ctx(), apiID, driver.CreateDeploymentInput{StageName: "s"}); err != nil {
		t.Fatal(err)
	}

	resp := invokeMock(t, m, apiID, driver.ProxyRequest{Body: `{"items":[{"id":1},{"id":2}]}`})
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d %s", resp.StatusCode, resp.Body)
	}
}

func TestMappingTemplateSizeQuota(t *testing.T) {
	m := newMock(t)
	apiID, resID := mockMethod(t, m, map[string]string{"application/json": `{"statusCode": 200}`}, "", "")
	big := strings.Repeat("x", 300<<10+1)

	_, err := m.UpdateIntegration(ctx(), apiID, resID, "GET", []driver.PatchOperation{
		{Op: "replace", Path: "/requestTemplates/application~1json", Value: big},
	})
	assertMessage(t, err, errors.IsInvalidArgument,
		"Mapping template for content type application/json exceeds the maximum size of 300 KB")

	_, err = m.UpdateIntegrationResponse(ctx(), apiID, resID, "GET", "200", []driver.PatchOperation{
		{Op: "add", Path: "/responseTemplates/text~1plain", Value: big},
	})
	assertMessage(t, err, errors.IsInvalidArgument,
		"Mapping template for content type text/plain exceeds the maximum size of 300 KB")
}

// TestMockJSONPathWalkHonoursDeadline runs a descent that multiplies over a
// deep, wide body. The template deadline must end it as a 500.
func TestMockJSONPathWalkHonoursDeadline(t *testing.T) {
	m := newMock(t)
	reqTmpl := `#set($x = $input.path('$..*..*..zz')){"statusCode": 200}`
	apiID, _ := mockMethod(t, m, map[string]string{"application/json": reqTmpl}, "", "")

	body := strings.Repeat("[1,2,3,", 900) + "0" + strings.Repeat("]", 900)
	start := time.Now()

	resp := invokeMock(t, m, apiID, driver.ProxyRequest{Body: body})
	if resp.StatusCode != 500 {
		t.Fatalf("status = %d %s", resp.StatusCode, resp.Body)
	}

	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("walk took %v", d)
	}
}

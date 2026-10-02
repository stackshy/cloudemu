package apigateway_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestClientCertificateWire(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL

	cc := doJSON(t, http.MethodPost, base+"/clientcertificates", `{"description":"d","tags":{"k":"v"}}`)
	id, _ := cc["clientCertificateId"].(string)
	pemCert, _ := cc["pemEncodedCertificate"].(string)

	if id == "" || !strings.HasPrefix(pemCert, "-----BEGIN CERTIFICATE-----") || cc["expirationDate"] == nil {
		t.Fatalf("GenerateClientCertificate = %v", cc)
	}

	doJSON(t, http.MethodPost, base+"/clientcertificates", `{}`)

	list := doJSON(t, http.MethodGet, base+"/clientcertificates?limit=1", "")
	if items, _ := list["item"].([]any); len(items) != 1 || list["position"] == nil {
		t.Fatalf("GetClientCertificates page = %v", list)
	}

	upd := doJSON(t, http.MethodPatch, base+"/clientcertificates/"+id,
		`{"patchOperations":[{"op":"replace","path":"/description","value":"new"}]}`)
	if upd["description"] != "new" {
		t.Fatalf("UpdateClientCertificate = %v", upd)
	}

	arn := url.PathEscape("arn:aws:apigateway:us-east-1::/clientcertificates/" + id)
	if status, raw, _ := doRaw(t, http.MethodPut, base+"/tags/"+arn, `{"tags":{"team":"a"}}`); status != http.StatusNoContent {
		t.Fatalf("TagResource = %d %s", status, raw)
	}

	if status, raw, _ := doRaw(t, http.MethodDelete, base+"/tags/"+arn+"?tagKeys=k", ""); status != http.StatusNoContent {
		t.Fatalf("UntagResource = %d %s", status, raw)
	}

	tags := doJSON(t, http.MethodGet, base+"/tags/"+arn, "")
	if got, _ := tags["tags"].(map[string]any); len(got) != 1 || got["team"] != "a" {
		t.Fatalf("GetTags = %v", tags)
	}

	apiID := buildProxyAPI(t, base)

	assertWireError(t, http.MethodPatch, base+"/restapis/"+apiID+"/stages/prod",
		`{"patchOperations":[{"op":"replace","path":"/clientCertificateId","value":"nosuch"}]}`,
		http.StatusNotFound, "NotFoundException", "Invalid Client Certificate identifier specified")

	st := doJSON(t, http.MethodPatch, base+"/restapis/"+apiID+"/stages/prod",
		`{"patchOperations":[{"op":"replace","path":"/clientCertificateId","value":"`+id+`"}]}`)
	if st["clientCertificateId"] != id {
		t.Fatalf("stage clientCertificateId = %v", st)
	}

	if status, _, errType := doRaw(t, http.MethodDelete, base+"/clientcertificates/"+id, ""); status != http.StatusBadRequest ||
		errType != "BadRequestException" {
		t.Fatalf("delete in-use certificate = %d %s", status, errType)
	}

	assertWireError(t, http.MethodGet, base+"/clientcertificates/nosuch", "",
		http.StatusNotFound, "NotFoundException", "Invalid Client Certificate identifier specified")
}

func TestDocumentationWire(t *testing.T) {
	srv := newE2E(t)
	base := srv.URL
	apiID := buildProxyAPI(t, base)
	parts := base + "/restapis/" + apiID + "/documentation/parts"

	p := doJSON(t, http.MethodPost, parts, `{"location":{"type":"RESOURCE"},"properties":"{\"description\":\"root\"}"}`)
	loc, _ := p["location"].(map[string]any)

	if loc["path"] != "/" || loc["type"] != "RESOURCE" {
		t.Fatalf("CreateDocumentationPart location = %v", p)
	}

	partID, _ := p["id"].(string)

	assertWireError(t, http.MethodPost, parts, `{"location":{"type":"RESOURCE","path":"/"},"properties":"{}"}`,
		http.StatusConflict, "ConflictException", "Documentation part already exists for the specified location: type 'RESOURCE', path '/'.")

	status, _, errType := doRaw(t, http.MethodPost, parts, `{"location":{"type":"API","method":"GET"},"properties":"{}"}`)
	if status != http.StatusBadRequest || errType != "BadRequestException" {
		t.Fatalf("invalid location field = %d %s", status, errType)
	}

	imported := doJSON(t, http.MethodPut, parts+"?mode=merge",
		`{"swagger":"2.0","x-amazon-apigateway-documentation":{"documentationParts":[`+
			`{"location":{"type":"API"},"properties":{"description":"api"}}]}}`)
	if ids, _ := imported["ids"].([]any); len(ids) != 1 {
		t.Fatalf("ImportDocumentationParts = %v", imported)
	}

	list := doJSON(t, http.MethodGet, parts+"?type=API", "")
	if items, _ := list["item"].([]any); len(items) != 1 {
		t.Fatalf("GetDocumentationParts type=API = %v", list)
	}

	upd := doJSON(t, http.MethodPatch, parts+"/"+partID,
		`{"patchOperations":[{"op":"replace","path":"/properties","value":"{\"description\":\"new\"}"}]}`)
	if upd["properties"] != `{"description":"new"}` {
		t.Fatalf("UpdateDocumentationPart = %v", upd)
	}

	versions := base + "/restapis/" + apiID + "/documentation/versions"

	v := doJSON(t, http.MethodPost, versions, `{"documentationVersion":"1.0","stageName":"prod","description":"first"}`)
	if v["version"] != "1.0" {
		t.Fatalf("CreateDocumentationVersion = %v", v)
	}

	st := doJSON(t, http.MethodGet, base+"/restapis/"+apiID+"/stages/prod", "")
	if st["documentationVersion"] != "1.0" {
		t.Fatalf("stage documentationVersion = %v", st)
	}

	assertWireError(t, http.MethodPost, versions, `{"documentationVersion":"1.0"}`,
		http.StatusConflict, "ConflictException", "Documentation version already exists")

	vl := doJSON(t, http.MethodGet, versions, "")
	if items, _ := vl["item"].([]any); len(items) != 1 {
		t.Fatalf("GetDocumentationVersions = %v", vl)
	}

	if status, raw, _ := doRaw(t, http.MethodDelete, parts+"/"+partID, ""); status != http.StatusAccepted {
		t.Fatalf("DeleteDocumentationPart = %d %s", status, raw)
	}

	assertWireError(t, http.MethodGet, versions+"/9", "",
		http.StatusNotFound, "NotFoundException", "Invalid Documentation version identifier specified")
}

func TestAccountWire(t *testing.T) {
	srv := newE2E(t)

	acct := doJSON(t, http.MethodGet, srv.URL+"/account", "")
	throttle, _ := acct["throttleSettings"].(map[string]any)

	if throttle["rateLimit"] != float64(10000) || throttle["burstLimit"] != float64(5000) || acct["apiKeyVersion"] != "4" {
		t.Fatalf("GetAccount = %v", acct)
	}

	role := "arn:aws:iam::123456789012:role/apigw"

	upd := doJSON(t, http.MethodPatch, srv.URL+"/account",
		`{"patchOperations":[{"op":"replace","path":"/cloudwatchRoleArn","value":"`+role+`"}]}`)
	if upd["cloudwatchRoleArn"] != role {
		t.Fatalf("UpdateAccount = %v", upd)
	}

	status, _, errType := doRaw(t, http.MethodPatch, srv.URL+"/account",
		`{"patchOperations":[{"op":"replace","path":"/apiKeyVersion","value":"3"}]}`)
	if status != http.StatusBadRequest || errType != "BadRequestException" {
		t.Fatalf("read-only account path = %d %s", status, errType)
	}
}

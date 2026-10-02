package apigateway_test

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

func TestGenerateClientCertificateIssuesSelfSignedPEM(t *testing.T) {
	m := newMock(t)

	cc, err := m.GenerateClientCertificate(ctx(), driver.GenerateClientCertificateInput{
		Description: "backend", Tags: map[string]string{"env": "dev"},
	})
	if err != nil {
		t.Fatalf("GenerateClientCertificate: %v", err)
	}

	if len(cc.ID) != 6 || cc.Description != "backend" || cc.Tags["env"] != "dev" {
		t.Fatalf("unexpected certificate: %+v", cc)
	}

	block, _ := pem.Decode([]byte(cc.PEMEncodedCertificate))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("pemEncodedCertificate is not a PEM certificate: %q", cc.PEMEncodedCertificate)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		t.Fatalf("certificate is not self-signed: %v", err)
	}

	if cc.ExpirationDate != cc.CreatedDate+int64(365*24*time.Hour/time.Second) {
		t.Fatalf("expirationDate %d, createdDate %d: want a 365-day validity", cc.ExpirationDate, cc.CreatedDate)
	}

	if cert.NotAfter.Unix() != cc.ExpirationDate {
		t.Fatalf("cert NotAfter %v does not match expirationDate %d", cert.NotAfter, cc.ExpirationDate)
	}
}

func TestClientCertificateLifecycle(t *testing.T) {
	m := newMock(t)

	cc, err := m.GenerateClientCertificate(ctx(), driver.GenerateClientCertificateInput{Description: "one"})
	if err != nil {
		t.Fatalf("GenerateClientCertificate: %v", err)
	}

	got, err := m.GetClientCertificate(ctx(), cc.ID)
	if err != nil || got.PEMEncodedCertificate != cc.PEMEncodedCertificate {
		t.Fatalf("GetClientCertificate = %+v, %v", got, err)
	}

	upd, err := m.UpdateClientCertificate(ctx(), cc.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/description", Value: "two"},
	})
	if err != nil || upd.Description != "two" {
		t.Fatalf("UpdateClientCertificate = %+v, %v", upd, err)
	}

	_, err = m.UpdateClientCertificate(ctx(), cc.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/pemEncodedCertificate", Value: "x"},
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("patching a read-only path = %v, want BadRequest", err)
	}

	if err := m.DeleteClientCertificate(ctx(), cc.ID); err != nil {
		t.Fatalf("DeleteClientCertificate: %v", err)
	}

	_, err = m.GetClientCertificate(ctx(), cc.ID)
	assertMessage(t, err, errors.IsNotFound, "Invalid Client Certificate identifier specified")

	assertMessage(t, m.DeleteClientCertificate(ctx(), cc.ID), errors.IsNotFound,
		"Invalid Client Certificate identifier specified")
}

func TestGetClientCertificatesPages(t *testing.T) {
	m := newMock(t)

	for range 3 {
		if _, err := m.GenerateClientCertificate(ctx(), driver.GenerateClientCertificateInput{}); err != nil {
			t.Fatalf("GenerateClientCertificate: %v", err)
		}
	}

	first, err := m.GetClientCertificates(ctx(), driver.PageInput{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.Position == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}

	second, err := m.GetClientCertificates(ctx(), driver.PageInput{Limit: 2, Position: first.Position})
	if err != nil || len(second.Items) != 1 || second.Position != "" {
		t.Fatalf("second page = %+v, %v", second, err)
	}

	if second.Items[0].ID == first.Items[0].ID || second.Items[0].ID == first.Items[1].ID {
		t.Fatalf("pages overlap: %+v / %+v", first.Items, second.Items)
	}

	if _, err := m.GetClientCertificates(ctx(), driver.PageInput{Limit: 501}); !errors.IsInvalidArgument(err) {
		t.Fatalf("limit 501 = %v, want BadRequest", err)
	}
}

func TestStageClientCertificateReference(t *testing.T) {
	m := newMock(t)
	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	_, err := m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/clientCertificateId", Value: "nosuch"},
	})
	assertMessage(t, err, errors.IsNotFound, "Invalid Client Certificate identifier specified")

	cc, err := m.GenerateClientCertificate(ctx(), driver.GenerateClientCertificateInput{})
	if err != nil {
		t.Fatalf("GenerateClientCertificate: %v", err)
	}

	st, err := m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/clientCertificateId", Value: cc.ID},
	})
	if err != nil || st.ClientCertificateID != cc.ID {
		t.Fatalf("attach certificate = %+v, %v", st, err)
	}

	err = m.DeleteClientCertificate(ctx(), cc.ID)
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("deleting an in-use certificate = %v, want BadRequest", err)
	}

	st, err = m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/clientCertificateId", Value: ""},
	})
	if err != nil || st.ClientCertificateID != "" {
		t.Fatalf("detach certificate = %+v, %v", st, err)
	}

	if err := m.DeleteClientCertificate(ctx(), cc.ID); err != nil {
		t.Fatalf("delete after detach: %v", err)
	}
}

func TestTagsOnClientCertificateAndRestAPI(t *testing.T) {
	m := newMock(t)

	cc, err := m.GenerateClientCertificate(ctx(), driver.GenerateClientCertificateInput{Tags: map[string]string{"a": "1"}})
	if err != nil {
		t.Fatalf("GenerateClientCertificate: %v", err)
	}

	certARN := "arn:aws:apigateway:us-east-1::/clientcertificates/" + cc.ID

	if err := m.TagResource(ctx(), certARN, map[string]string{"b": "2"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	if err := m.UntagResource(ctx(), certARN, []string{"a"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, err := m.GetTags(ctx(), certARN)
	if err != nil || len(tags) != 1 || tags["b"] != "2" {
		t.Fatalf("GetTags = %v, %v", tags, err)
	}

	got, _ := m.GetClientCertificate(ctx(), cc.ID)
	if len(got.Tags) != 1 || got.Tags["b"] != "2" {
		t.Fatalf("certificate tags = %v", got.Tags)
	}

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "t"})
	if err != nil {
		t.Fatalf("CreateRestAPI: %v", err)
	}

	apiARN := "arn:aws:apigateway:us-east-1::/restapis/" + api.ID
	if err := m.TagResource(ctx(), apiARN, map[string]string{"team": "x"}); err != nil {
		t.Fatalf("TagResource api: %v", err)
	}

	if a, _ := m.GetRestAPI(ctx(), api.ID); a.Tags["team"] != "x" {
		t.Fatalf("rest api tags = %v", a.Tags)
	}

	if _, err := m.GetTags(ctx(), "arn:aws:apigateway:us-east-1::/clientcertificates/nosuch"); !errors.IsNotFound(err) {
		t.Fatalf("GetTags on a missing certificate = %v, want NotFound", err)
	}

	if err := m.TagResource(ctx(), "not-an-arn", map[string]string{"k": "v"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("TagResource bad ARN = %v, want BadRequest", err)
	}
}

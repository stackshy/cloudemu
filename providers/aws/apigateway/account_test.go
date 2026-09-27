package apigateway_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

func TestGetAccountDefaults(t *testing.T) {
	m := newMock(t)

	acct, err := m.GetAccount(ctx())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}

	if acct.CloudWatchRoleARN != "" || acct.Throttle.BurstLimit != 5000 || acct.Throttle.RateLimit != 10000 {
		t.Fatalf("defaults = %+v", acct)
	}

	if len(acct.Features) != 1 || acct.Features[0] != "UsagePlans" || acct.APIKeyVersion != "4" {
		t.Fatalf("defaults = %+v", acct)
	}
}

func TestUpdateAccount(t *testing.T) {
	m := newMock(t)
	role := "arn:aws:iam::000000000000:role/apigw-logs"

	acct, err := m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/cloudwatchRoleArn", Value: role}})
	if err != nil || acct.CloudWatchRoleARN != role {
		t.Fatalf("set role = %+v, %v", acct, err)
	}

	if got, _ := m.GetAccount(ctx()); got.CloudWatchRoleARN != role {
		t.Fatalf("role not persisted: %+v", got)
	}

	_, err = m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/cloudwatchRoleArn", Value: "not-an-arn"}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("bad role ARN = %v, want BadRequest", err)
	}

	acct, err = m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "remove", Path: "/features", Value: "UsagePlans"}})
	if err != nil || len(acct.Features) != 0 {
		t.Fatalf("remove feature = %+v, %v", acct, err)
	}

	acct, err = m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "add", Path: "/features", Value: "UsagePlans"}})
	if err != nil || len(acct.Features) != 1 {
		t.Fatalf("add feature = %+v, %v", acct, err)
	}

	_, err = m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "add", Path: "/features", Value: "Bogus"}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown feature = %v, want BadRequest", err)
	}

	_, err = m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/throttle/rateLimit", Value: "1"}})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("read-only path = %v, want BadRequest", err)
	}

	acct, err = m.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/cloudwatchRoleArn", Value: ""}})
	if err != nil || acct.CloudWatchRoleARN != "" {
		t.Fatalf("clear role = %+v, %v", acct, err)
	}
}

func TestSnapshotCarriesCertificatesDocumentationAndAccount(t *testing.T) {
	src := newMock(t)
	apiID, _, _ := deployProxyAPI(t, src, "hello", "GET", lambdaURI)

	cc, err := src.GenerateClientCertificate(ctx(), driver.GenerateClientCertificateInput{Description: "kept"})
	if err != nil {
		t.Fatalf("GenerateClientCertificate: %v", err)
	}

	part := mustPart(t, src, apiID, driver.DocumentationPartLocation{Type: "API"}, `{"description":"d"}`)

	if _, err := src.CreateDocumentationVersion(ctx(), apiID, driver.CreateDocumentationVersionInput{
		Version: "1", StageName: "prod",
	}); err != nil {
		t.Fatalf("CreateDocumentationVersion: %v", err)
	}

	if _, err := src.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/clientCertificateId", Value: cc.ID},
	}); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}

	role := "arn:aws:iam::000000000000:role/r"
	if _, err := src.UpdateAccount(ctx(), []driver.PatchOperation{{Op: "replace", Path: "/cloudwatchRoleArn", Value: role}}); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	data, err := src.Snapshot(ctx(), false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx(), data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := dst.GetClientCertificate(ctx(), cc.ID)
	if err != nil || got.PEMEncodedCertificate != cc.PEMEncodedCertificate || got.Description != "kept" {
		t.Fatalf("restored certificate = %+v, %v", got, err)
	}

	if _, err := dst.GetDocumentationPart(ctx(), apiID, part.ID); err != nil {
		t.Fatalf("restored part: %v", err)
	}

	if _, err := dst.GetDocumentationVersion(ctx(), apiID, "1"); err != nil {
		t.Fatalf("restored version: %v", err)
	}

	st, _ := dst.GetStage(ctx(), apiID, "prod")
	if st.ClientCertificateID != cc.ID || st.DocumentationVersion != "1" {
		t.Fatalf("restored stage = %+v", st)
	}

	if acct, _ := dst.GetAccount(ctx()); acct.CloudWatchRoleARN != role {
		t.Fatalf("restored account = %+v", acct)
	}

	// The restored stage still pins the certificate.
	if err := dst.DeleteClientCertificate(ctx(), cc.ID); !errors.IsInvalidArgument(err) {
		t.Fatalf("delete of restored in-use certificate = %v, want BadRequest", err)
	}
}

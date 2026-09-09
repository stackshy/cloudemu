package aoss_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/aoss"
	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

func newMock() *aoss.Mock {
	return aoss.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireError(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

const encPolicy = `{"Rules":[{"ResourceType":"collection","Resource":["collection/logs-app"]}],"AWSOwnedKey":true}`

func seedEncryption(t *testing.T, m *aoss.Mock, name string) {
	t.Helper()

	_, err := m.CreateSecurityPolicy(context.Background(), &driver.CreatePolicyInput{
		Type:   driver.SecurityPolicyEncryption,
		Name:   name,
		Policy: json.RawMessage(encPolicy),
	})
	requireNoError(t, err)
}

func TestCreateCollectionRequiresEncryptionPolicy(t *testing.T) {
	m := newMock()

	_, err := m.CreateCollection(context.Background(), &driver.CreateCollectionInput{
		Name: "logs-app", Type: driver.CollectionTypeSearch,
	})
	requireError(t, err)

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || apiErr.Exception != driver.ExValidation {
		t.Fatalf("expected ValidationException, got %v", err)
	}
}

func TestCreateCollectionComputedFieldsStable(t *testing.T) {
	m := newMock()
	seedEncryption(t, m, "enc")

	col, err := m.CreateCollection(context.Background(), &driver.CreateCollectionInput{
		Name: "logs-app", Type: driver.CollectionTypeSearch, Description: "d",
	})
	requireNoError(t, err)

	assertEqual(t, col.Status, driver.StatusActive)
	assertEqual(t, col.Type, driver.CollectionTypeSearch)
	assertEqual(t, col.KmsKeyArn, "auto")
	assertEqual(t, col.StandbyReplicas, "ENABLED")

	if !strings.HasPrefix(col.ARN, "arn:aws:aoss:") || !strings.Contains(col.ARN, ":collection/"+col.ID) {
		t.Fatalf("unexpected arn %q", col.ARN)
	}

	wantEndpoint := "https://" + col.ID + "." + config.NewOptions().Region + ".aoss.amazonaws.com"
	assertEqual(t, col.CollectionEndpoint, wantEndpoint)
	assertEqual(t, col.DashboardEndpoint, wantEndpoint+"/_dashboards")

	// Computed fields must be byte-stable across every read.
	details, errs, err := m.BatchGetCollection(context.Background(), []string{col.ID}, nil)
	requireNoError(t, err)
	assertEqual(t, len(errs), 0)
	assertEqual(t, len(details), 1)
	got := details[0]
	assertEqual(t, got.ID, col.ID)
	assertEqual(t, got.ARN, col.ARN)
	assertEqual(t, got.CollectionEndpoint, col.CollectionEndpoint)
	assertEqual(t, got.DashboardEndpoint, col.DashboardEndpoint)
	assertEqual(t, got.CreatedDate.Equal(col.CreatedDate), true)
	assertEqual(t, got.Status, driver.StatusActive)
}

func TestUpdateCollectionPreservesComputed(t *testing.T) {
	m := newMock()
	seedEncryption(t, m, "enc")

	col, err := m.CreateCollection(context.Background(), &driver.CreateCollectionInput{
		Name: "logs-app", Type: driver.CollectionTypeSearch,
	})
	requireNoError(t, err)

	newDesc := "updated"
	upd, err := m.UpdateCollection(context.Background(), &driver.UpdateCollectionInput{ID: col.ID, Description: &newDesc})
	requireNoError(t, err)

	assertEqual(t, upd.Description, "updated")
	assertEqual(t, upd.ARN, col.ARN)
	assertEqual(t, upd.ID, col.ID)
	assertEqual(t, upd.CreatedDate.Equal(col.CreatedDate), true)
}

func TestDeleteCollectionThen404(t *testing.T) {
	m := newMock()
	seedEncryption(t, m, "enc")

	col, err := m.CreateCollection(context.Background(), &driver.CreateCollectionInput{
		Name: "logs-app", Type: driver.CollectionTypeSearch,
	})
	requireNoError(t, err)

	del, err := m.DeleteCollection(context.Background(), col.ID)
	requireNoError(t, err)
	assertEqual(t, del.Status, driver.StatusDeleting)

	details, errs, err := m.BatchGetCollection(context.Background(), []string{col.ID}, nil)
	requireNoError(t, err)
	assertEqual(t, len(details), 0)
	assertEqual(t, len(errs), 1)
}

func TestSecurityPolicyLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateSecurityPolicy(ctx, &driver.CreatePolicyInput{
		Type: driver.SecurityPolicyEncryption, Name: "enc", Description: "d",
		Policy: json.RawMessage(encPolicy),
	})
	requireNoError(t, err)

	if len(created.PolicyVersion) < 20 || len(created.PolicyVersion) > 36 {
		t.Fatalf("policyVersion %q out of documented length range", created.PolicyVersion)
	}

	// Duplicate create conflicts.
	_, err = m.CreateSecurityPolicy(ctx, &driver.CreatePolicyInput{
		Type: driver.SecurityPolicyEncryption, Name: "enc", Policy: json.RawMessage(encPolicy),
	})
	requireError(t, err)

	got, err := m.GetSecurityPolicy(ctx, driver.SecurityPolicyEncryption, "enc")
	requireNoError(t, err)
	assertEqual(t, got.PolicyVersion, created.PolicyVersion)

	// Update mints a new version.
	newDesc := "d2"
	upd, err := m.UpdateSecurityPolicy(ctx, &driver.UpdatePolicyInput{
		Type: driver.SecurityPolicyEncryption, Name: "enc", Description: &newDesc, PolicyVersion: created.PolicyVersion,
	})
	requireNoError(t, err)
	assertEqual(t, upd.Description, "d2")

	if upd.PolicyVersion == created.PolicyVersion {
		t.Fatalf("expected policyVersion to change on update")
	}

	requireNoError(t, m.DeleteSecurityPolicy(ctx, driver.SecurityPolicyEncryption, "enc"))
	_, err = m.GetSecurityPolicy(ctx, driver.SecurityPolicyEncryption, "enc")
	requireError(t, err)
}

func TestAccessPolicyRejectsBadType(t *testing.T) {
	m := newMock()

	_, err := m.CreateAccessPolicy(context.Background(), &driver.CreatePolicyInput{
		Type: "encryption", Name: "p", Policy: json.RawMessage(`{}`),
	})
	requireError(t, err)
}

func TestTagLifecycle(t *testing.T) {
	m := newMock()
	seedEncryption(t, m, "enc")
	ctx := context.Background()

	col, err := m.CreateCollection(ctx, &driver.CreateCollectionInput{
		Name: "logs-app", Type: driver.CollectionTypeSearch, Tags: []driver.Tag{{Key: "env", Value: "dev"}},
	})
	requireNoError(t, err)

	requireNoError(t, m.TagResource(ctx, col.ARN, []driver.Tag{{Key: "team", Value: "ops"}, {Key: "env", Value: "prod"}}))

	tags, err := m.ListTagsForResource(ctx, col.ARN)
	requireNoError(t, err)
	assertEqual(t, len(tags), 2)
	assertEqual(t, tagValue(tags, "env"), "prod")
	assertEqual(t, tagValue(tags, "team"), "ops")

	requireNoError(t, m.UntagResource(ctx, col.ARN, []string{"team"}))
	tags, err = m.ListTagsForResource(ctx, col.ARN)
	requireNoError(t, err)
	assertEqual(t, len(tags), 1)
}

func TestSnapshotRestore(t *testing.T) {
	m := newMock()
	seedEncryption(t, m, "enc")
	ctx := context.Background()

	col, err := m.CreateCollection(ctx, &driver.CreateCollectionInput{Name: "logs-app", Type: driver.CollectionTypeSearch})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	details, _, err := restored.BatchGetCollection(ctx, []string{col.ID}, nil)
	requireNoError(t, err)
	assertEqual(t, len(details), 1)
	assertEqual(t, details[0].ARN, col.ARN)
}

func tagValue(tags []driver.Tag, key string) string {
	for _, t := range tags {
		if t.Key == key {
			return t.Value
		}
	}

	return ""
}

func isAPIError(err error, target **driver.APIError) bool {
	for e := err; e != nil; {
		if ae, ok := e.(*driver.APIError); ok {
			*target = ae

			return true
		}

		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}

		e = u.Unwrap()
	}

	return false
}

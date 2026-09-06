package appflow_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/appflow"
	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

func newMock() *appflow.Mock {
	return appflow.New(config.NewOptions())
}

func sourceExtra() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"sourceFlowConfig":          json.RawMessage(`{"connectorType":"S3"}`),
		"destinationFlowConfigList": json.RawMessage(`[{"connectorType":"S3"}]`),
		"triggerConfig":             json.RawMessage(`{"triggerType":"OnDemand"}`),
		"tasks":                     json.RawMessage(`[{"taskType":"Map"}]`),
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateDescribeStable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateFlow(ctx, &driver.CreateFlowInput{
		FlowName:    "flow1",
		Description: "d",
		Extra:       sourceExtra(),
	})
	requireNoError(t, err)

	if created.FlowStatus != driver.FlowStatusActive {
		t.Fatalf("status = %q, want Active", created.FlowStatus)
	}

	if created.SourceConnectorType != "S3" || created.DestinationConnectorType != "S3" ||
		created.TriggerType != "OnDemand" {
		t.Fatalf("summary types not derived: %+v", created)
	}

	d1, err := m.DescribeFlow(ctx, "flow1")
	requireNoError(t, err)

	d2, err := m.DescribeFlow(ctx, "flow1")
	requireNoError(t, err)

	if d1.FlowArn != d2.FlowArn || d1.FlowArn != created.FlowArn {
		t.Fatal("flowArn not stable across reads")
	}

	if !d1.CreatedAt.Equal(d2.CreatedAt) {
		t.Fatal("createdAt not stable across reads")
	}
}

func TestUpdatePreservesComputed(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateFlow(ctx, &driver.CreateFlowInput{FlowName: "f", Extra: sourceExtra()})
	requireNoError(t, err)

	updated, err := m.UpdateFlow(ctx, &driver.UpdateFlowInput{
		FlowName:    "f",
		Description: "new",
		Extra:       sourceExtra(),
	})
	requireNoError(t, err)

	if updated.FlowArn != created.FlowArn {
		t.Fatal("flowArn drifted on update")
	}

	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("createdAt drifted on update")
	}

	if updated.Description != "new" {
		t.Fatalf("description = %q, want new", updated.Description)
	}
}

func TestDuplicateConflict(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateFlow(ctx, &driver.CreateFlowInput{FlowName: "dup", Extra: sourceExtra()})
	requireNoError(t, err)

	_, err = m.CreateFlow(ctx, &driver.CreateFlowInput{FlowName: "dup", Extra: sourceExtra()})

	var apiErr *driver.APIError
	if !asAPIError(err, &apiErr) || apiErr.Exception != driver.ExConflict {
		t.Fatalf("got %v, want ConflictException", err)
	}
}

func TestDescribeMissing(t *testing.T) {
	m := newMock()

	_, err := m.DescribeFlow(context.Background(), "nope")

	var apiErr *driver.APIError
	if !asAPIError(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("got %v, want ResourceNotFoundException", err)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateFlow(ctx, &driver.CreateFlowInput{
		FlowName: "tf",
		Tags:     map[string]string{"a": "1"},
		Extra:    sourceExtra(),
	})
	requireNoError(t, err)

	requireNoError(t, m.TagResource(ctx, created.FlowArn, map[string]string{"b": "2"}))

	tags, err := m.ListTagsForResource(ctx, created.FlowArn)
	requireNoError(t, err)

	if tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("tags = %v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, created.FlowArn, []string{"a"}))

	tags2, _ := m.ListTagsForResource(ctx, created.FlowArn)
	if _, ok := tags2["a"]; ok {
		t.Fatal("tag a not removed")
	}
}

func TestConnectorProfileLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	p, err := m.CreateConnectorProfile(ctx, &driver.CreateConnectorProfileInput{
		ConnectorProfileName: "cp",
		ConnectorType:        "Salesforce",
		ConnectionMode:       "Public",
	})
	requireNoError(t, err)

	if p.ConnectorProfileArn == "" || p.CredentialsArn == "" {
		t.Fatal("computed arns empty")
	}

	profiles, _, err := m.DescribeConnectorProfiles(ctx, nil, "", driver.Page{})
	requireNoError(t, err)

	if len(profiles) != 1 {
		t.Fatalf("profiles len = %d, want 1", len(profiles))
	}

	requireNoError(t, m.DeleteConnectorProfile(ctx, "cp", false))
}

// asAPIError is a local errors.As shim so provider tests avoid importing
// stdlib errors solely for one assertion helper.
func asAPIError(err error, target **driver.APIError) bool {
	for err != nil {
		if e, ok := err.(*driver.APIError); ok {
			*target = e

			return true
		}

		type unwrapper interface{ Unwrap() error }

		u, ok := err.(unwrapper)
		if !ok {
			return false
		}

		err = u.Unwrap()
	}

	return false
}

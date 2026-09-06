package mwaa_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/mwaa"
	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

func newMock() *mwaa.Mock {
	return mwaa.New(config.NewOptions())
}

func baseConfig() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"AirflowVersion":   json.RawMessage(`"2.10.1"`),
		"DagS3Path":        json.RawMessage(`"dags"`),
		"ExecutionRoleArn": json.RawMessage(`"arn:aws:iam::123456789012:role/exec"`),
		"SourceBucketArn":  json.RawMessage(`"arn:aws:s3:::bucket"`),
		"MaxWorkers":       json.RawMessage(`10`),
		"MinWorkers":       json.RawMessage(`1`),
		"NetworkConfiguration": json.RawMessage(
			`{"SecurityGroupIds":["sg-1"],"SubnetIds":["subnet-1","subnet-2"]}`),
		"LoggingConfiguration": json.RawMessage(
			`{"DagProcessingLogs":{"Enabled":true,"LogLevel":"INFO"},` +
				`"WebserverLogs":{"Enabled":false,"LogLevel":"ERROR"}}`),
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func create(t *testing.T, m *mwaa.Mock, name string) *driver.Environment {
	t.Helper()

	out, err := m.CreateEnvironment(context.Background(), &driver.CreateEnvironmentInput{
		Name:   name,
		Tags:   map[string]string{"a": "1"},
		Config: baseConfig(),
	})
	requireNoError(t, err)

	return out
}

func TestCreateGetStable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created := create(t, m, "env1")

	if created.Status != driver.StatusAvailable {
		t.Fatalf("status = %q, want AVAILABLE", created.Status)
	}

	if created.Arn == "" || created.WebserverURL == "" || created.ServiceRoleArn == "" {
		t.Fatalf("computed fields empty: %+v", created)
	}

	g1, err := m.GetEnvironment(ctx, "env1")
	requireNoError(t, err)

	g2, err := m.GetEnvironment(ctx, "env1")
	requireNoError(t, err)

	if g1.Arn != g2.Arn || g1.Arn != created.Arn {
		t.Fatal("Arn not stable across reads")
	}

	if g1.WebserverURL != g2.WebserverURL || g1.ServiceRoleArn != g2.ServiceRoleArn {
		t.Fatal("computed fields not stable across reads")
	}

	if !g1.CreatedAt.Equal(g2.CreatedAt) {
		t.Fatal("CreatedAt not stable across reads")
	}

	if string(g1.Config["LoggingConfiguration"]) != string(g2.Config["LoggingConfiguration"]) {
		t.Fatal("LoggingConfiguration not byte-stable across reads")
	}
}

func TestLoggingAugmentation(t *testing.T) {
	m := newMock()

	created := create(t, m, "logenv")

	var logging map[string]struct {
		Enabled               bool   `json:"Enabled"`
		LogLevel              string `json:"LogLevel"`
		CloudWatchLogGroupArn string `json:"CloudWatchLogGroupArn"`
	}
	requireNoError(t, json.Unmarshal(created.Config["LoggingConfiguration"], &logging))

	if len(logging) != 5 {
		t.Fatalf("logging modules = %d, want 5", len(logging))
	}

	if !logging["DagProcessingLogs"].Enabled || logging["DagProcessingLogs"].CloudWatchLogGroupArn == "" {
		t.Fatal("enabled DagProcessingLogs missing arn")
	}

	if logging["WebserverLogs"].Enabled || logging["WebserverLogs"].CloudWatchLogGroupArn != "" {
		t.Fatal("disabled WebserverLogs should have no arn")
	}

	if !logging["WorkerLogs"].Enabled && logging["WorkerLogs"].LogLevel != "INFO" {
		t.Fatalf("unconfigured WorkerLogs default = %+v", logging["WorkerLogs"])
	}
}

func TestUpdateMergesAndPreserves(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created := create(t, m, "u")

	updated, err := m.UpdateEnvironment(ctx, &driver.UpdateEnvironmentInput{
		Name: "u",
		Config: map[string]json.RawMessage{
			"MaxWorkers":                json.RawMessage(`20`),
			"NetworkConfiguration":      json.RawMessage(`{"SecurityGroupIds":["sg-2"]}`),
			"WorkerReplacementStrategy": json.RawMessage(`"FORCED"`),
		},
	})
	requireNoError(t, err)

	if updated.Arn != created.Arn || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("computed fields drifted on update")
	}

	if string(updated.Config["MaxWorkers"]) != "20" {
		t.Fatalf("MaxWorkers = %s, want 20", updated.Config["MaxWorkers"])
	}

	// Unmentioned field survives.
	if string(updated.Config["AirflowVersion"]) != `"2.10.1"` {
		t.Fatal("AirflowVersion lost on update")
	}

	// WorkerReplacementStrategy is a directive, never stored.
	if _, ok := updated.Config["WorkerReplacementStrategy"]; ok {
		t.Fatal("WorkerReplacementStrategy should not be stored")
	}

	// Deep-merge preserves the immutable subnet ids.
	var net struct {
		SecurityGroupIds []string `json:"SecurityGroupIds"`
		SubnetIds        []string `json:"SubnetIds"`
	}
	requireNoError(t, json.Unmarshal(updated.Config["NetworkConfiguration"], &net))

	if len(net.SecurityGroupIds) != 1 || net.SecurityGroupIds[0] != "sg-2" {
		t.Fatalf("SecurityGroupIds = %v, want [sg-2]", net.SecurityGroupIds)
	}

	if len(net.SubnetIds) != 2 {
		t.Fatalf("SubnetIds = %v, want 2 preserved", net.SubnetIds)
	}
}

func TestDuplicateValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	create(t, m, "dup")

	_, err := m.CreateEnvironment(ctx, &driver.CreateEnvironmentInput{Name: "dup", Config: baseConfig()})

	var apiErr *driver.APIError
	if !asAPIError(err, &apiErr) || apiErr.Exception != driver.ExValidation {
		t.Fatalf("got %v, want ValidationException", err)
	}
}

func TestGetMissing(t *testing.T) {
	m := newMock()

	_, err := m.GetEnvironment(context.Background(), "nope")

	var apiErr *driver.APIError
	if !asAPIError(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("got %v, want ResourceNotFoundException", err)
	}
}

func TestListAndDelete(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	create(t, m, "b-env")
	create(t, m, "a-env")

	names, _, err := m.ListEnvironments(ctx, driver.Page{})
	requireNoError(t, err)

	if len(names) != 2 || names[0] != "a-env" || names[1] != "b-env" {
		t.Fatalf("names = %v, want sorted [a-env b-env]", names)
	}

	requireNoError(t, m.DeleteEnvironment(ctx, "a-env"))

	_, err = m.GetEnvironment(ctx, "a-env")
	if err == nil {
		t.Fatal("expected not-found after delete")
	}
}

func TestTagsRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created := create(t, m, "tf")

	requireNoError(t, m.TagResource(ctx, created.Arn, map[string]string{"b": "2"}))

	tags, err := m.ListTagsForResource(ctx, created.Arn)
	requireNoError(t, err)

	if tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("tags = %v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, created.Arn, []string{"a"}))

	tags2, _ := m.ListTagsForResource(ctx, created.Arn)
	if _, ok := tags2["a"]; ok {
		t.Fatal("tag a not removed")
	}
}

func TestTokens(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	create(t, m, "tok")

	cli, host, err := m.CreateCliToken(ctx, "tok")
	requireNoError(t, err)

	if cli == "" || host == "" {
		t.Fatal("cli token or host empty")
	}

	// Token is stable across calls.
	cli2, _, err := m.CreateCliToken(ctx, "tok")
	requireNoError(t, err)

	if cli != cli2 {
		t.Fatal("cli token not stable")
	}

	_, _, err = m.CreateWebLoginToken(ctx, "missing")
	if err == nil {
		t.Fatal("expected not-found for missing env")
	}
}

// asAPIError is a local errors.As shim so provider tests avoid importing stdlib
// errors solely for one assertion helper.
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

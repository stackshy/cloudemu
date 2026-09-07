package mq_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/mq"
	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

func newMock() *mq.Mock {
	return mq.New(config.NewOptions())
}

func brokerConfig() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"brokerName":         json.RawMessage(`"test-broker"`),
		"engineType":         json.RawMessage(`"ActiveMQ"`),
		"engineVersion":      json.RawMessage(`"5.17.6"`),
		"hostInstanceType":   json.RawMessage(`"mq.t3.micro"`),
		"deploymentMode":     json.RawMessage(`"SINGLE_INSTANCE"`),
		"publiclyAccessible": json.RawMessage(`false`),
		"securityGroups":     json.RawMessage(`["sg-1"]`),
		"subnetIds":          json.RawMessage(`["subnet-1"]`),
		"logs":               json.RawMessage(`{"general":true,"audit":false}`),
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createBroker(t *testing.T, m *mq.Mock) *driver.Broker {
	t.Helper()

	b, err := m.CreateBroker(context.Background(), &driver.CreateBrokerInput{
		BrokerName: "test-broker",
		EngineType: "ActiveMQ",
		Config:     brokerConfig(),
		Users:      []driver.User{{Username: "admin", Password: "SuperSecret1234", ConsoleAccess: true}},
	})
	requireNoError(t, err)

	return b
}

func TestCreateBrokerRunningAndComputed(t *testing.T) {
	m := newMock()
	b := createBroker(t, m)

	if b.BrokerState != driver.StateRunning {
		t.Fatalf("state = %q, want RUNNING", b.BrokerState)
	}

	if !strings.HasPrefix(b.BrokerID, "b-") {
		t.Fatalf("brokerId = %q, want b- prefix", b.BrokerID)
	}

	if !strings.Contains(b.BrokerArn, ":mq:") || !strings.Contains(b.BrokerArn, "broker:"+b.BrokerID) {
		t.Fatalf("brokerArn = %q", b.BrokerArn)
	}

	if len(b.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(b.Instances))
	}

	inst := b.Instances[0]
	if !strings.HasPrefix(inst.ConsoleURL, "https://"+b.BrokerID+".mq.") || !strings.HasSuffix(inst.ConsoleURL, ":8162") {
		t.Fatalf("consoleURL = %q", inst.ConsoleURL)
	}

	if len(inst.Endpoints) != 5 {
		t.Fatalf("endpoints = %v, want 5 ActiveMQ protocol endpoints", inst.Endpoints)
	}

	if inst.IPAddress == "" {
		t.Fatal("ActiveMQ instance should report an ipAddress")
	}
}

func TestDescribeBrokerStable(t *testing.T) {
	m := newMock()
	b := createBroker(t, m)

	d1, err := m.DescribeBroker(context.Background(), b.BrokerID)
	requireNoError(t, err)

	d2, err := m.DescribeBroker(context.Background(), b.BrokerID)
	requireNoError(t, err)

	if d1.BrokerArn != d2.BrokerArn || d1.BrokerArn != b.BrokerArn {
		t.Fatal("brokerArn drifted across reads")
	}

	if !d1.Created.Equal(d2.Created) {
		t.Fatal("created drifted across reads")
	}

	if d1.Instances[0].ConsoleURL != d2.Instances[0].ConsoleURL {
		t.Fatal("consoleURL drifted across reads")
	}

	if len(d1.Instances[0].Endpoints) != len(d2.Instances[0].Endpoints) {
		t.Fatal("endpoints drifted across reads")
	}
}

func TestDuplicateBrokerNameConflict(t *testing.T) {
	m := newMock()
	createBroker(t, m)

	_, err := m.CreateBroker(context.Background(), &driver.CreateBrokerInput{
		BrokerName: "test-broker",
		EngineType: "ActiveMQ",
		Config:     brokerConfig(),
	})

	var apiErr *driver.APIError
	if !asAPIError(err, &apiErr) || apiErr.Exception != driver.ExConflict {
		t.Fatalf("duplicate name: got %v, want ConflictException", err)
	}
}

func TestUpdateBrokerMergesAndPreservesComputed(t *testing.T) {
	m := newMock()
	b := createBroker(t, m)

	updated, err := m.UpdateBroker(context.Background(), &driver.UpdateBrokerInput{
		BrokerID: b.BrokerID,
		Config:   map[string]json.RawMessage{"logs": json.RawMessage(`{"general":true,"audit":true}`)},
	})
	requireNoError(t, err)

	if updated.BrokerArn != b.BrokerArn || !updated.Created.Equal(b.Created) {
		t.Fatal("computed fields drifted after update")
	}

	if string(updated.Config["logs"]) != `{"general":true,"audit":true}` {
		t.Fatalf("logs not merged: %s", updated.Config["logs"])
	}

	// Unmentioned fields survive.
	if string(updated.Config["engineVersion"]) != `"5.17.6"` {
		t.Fatal("engineVersion lost on update")
	}
}

func TestDeleteBroker(t *testing.T) {
	m := newMock()
	b := createBroker(t, m)

	requireNoError(t, m.DeleteBroker(context.Background(), b.BrokerID))

	_, err := m.DescribeBroker(context.Background(), b.BrokerID)

	var apiErr *driver.APIError
	if !asAPIError(err, &apiErr) || apiErr.Exception != driver.ExNotFound {
		t.Fatalf("describe after delete: got %v, want NotFoundException", err)
	}
}

func TestRabbitMQEndpoints(t *testing.T) {
	m := newMock()

	b, err := m.CreateBroker(context.Background(), &driver.CreateBrokerInput{
		BrokerName: "rabbit",
		EngineType: "RabbitMQ",
		Config: map[string]json.RawMessage{
			"brokerName":     json.RawMessage(`"rabbit"`),
			"engineType":     json.RawMessage(`"RabbitMQ"`),
			"deploymentMode": json.RawMessage(`"SINGLE_INSTANCE"`),
		},
	})
	requireNoError(t, err)

	inst := b.Instances[0]
	if len(inst.Endpoints) != 1 || !strings.HasPrefix(inst.Endpoints[0], "amqps://") {
		t.Fatalf("RabbitMQ endpoints = %v, want single amqps endpoint", inst.Endpoints)
	}

	if inst.IPAddress != "" {
		t.Fatal("RabbitMQ broker should report no ipAddress")
	}
}

func TestConfigurationLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	c, err := m.CreateConfiguration(ctx, &driver.CreateConfigurationInput{
		Name:          "cfg",
		EngineType:    "ActiveMQ",
		EngineVersion: "5.17.6",
	})
	requireNoError(t, err)

	if !strings.HasPrefix(c.ID, "c-") || c.LatestRevision().Revision != 1 {
		t.Fatalf("create: id=%q rev=%d", c.ID, c.LatestRevision().Revision)
	}

	data := "PGJyb2tlcj48L2Jyb2tlcj4=" // base64("<broker></broker>")

	up, err := m.UpdateConfiguration(ctx, &driver.UpdateConfigurationInput{
		ConfigurationID: c.ID,
		Data:            data,
		Description:     "rev2",
	})
	requireNoError(t, err)

	if up.LatestRevision().Revision != 2 {
		t.Fatalf("latest revision = %d, want 2", up.LatestRevision().Revision)
	}

	// Stable id/arn across reads.
	got, err := m.DescribeConfiguration(ctx, c.ID)
	requireNoError(t, err)

	if got.ID != c.ID || got.Arn != c.Arn || got.LatestRevision().Revision != 2 {
		t.Fatal("configuration drifted after update")
	}

	rev, err := m.DescribeConfigurationRevision(ctx, c.ID, 2)
	requireNoError(t, err)

	if rev.Data != data {
		t.Fatalf("revision data = %q, want verbatim round-trip", rev.Data)
	}
}

func TestUserPasswordWriteOnly(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	b := createBroker(t, m)

	requireNoError(t, m.CreateUser(ctx, b.BrokerID, &driver.User{
		Username: "app", Password: "AnotherSecret99", Groups: []string{"dev"},
	}))

	u, err := m.DescribeUser(ctx, b.BrokerID, "app")
	requireNoError(t, err)

	if u.Password != "" {
		t.Fatal("DescribeUser must never echo the password")
	}

	if len(u.Groups) != 1 || u.Groups[0] != "dev" {
		t.Fatalf("groups = %v", u.Groups)
	}

	// Update preserving password (empty password keeps stored one).
	requireNoError(t, m.UpdateUser(ctx, b.BrokerID, &driver.User{
		Username: "app", Groups: []string{"dev", "ops"},
	}))

	users, _, err := m.ListUsers(ctx, b.BrokerID, driver.Page{})
	requireNoError(t, err)

	if len(users) != 2 {
		t.Fatalf("users = %d, want 2 (admin + app)", len(users))
	}

	requireNoError(t, m.DeleteUser(ctx, b.BrokerID, "app"))

	_, err = m.DescribeUser(ctx, b.BrokerID, "app")
	if err == nil {
		t.Fatal("DescribeUser after delete should fail")
	}
}

func TestTagsBrokerAndConfiguration(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	b := createBroker(t, m)

	requireNoError(t, m.CreateTags(ctx, b.BrokerArn, map[string]string{"env": "test"}))

	tags, err := m.ListTags(ctx, b.BrokerArn)
	requireNoError(t, err)

	if tags["env"] != "test" {
		t.Fatalf("broker tags = %v", tags)
	}

	requireNoError(t, m.DeleteTags(ctx, b.BrokerArn, []string{"env"}))

	tags, err = m.ListTags(ctx, b.BrokerArn)
	requireNoError(t, err)

	if _, ok := tags["env"]; ok {
		t.Fatal("env tag not removed")
	}

	_, err = m.ListTags(ctx, "arn:aws:s3:::not-mq")
	if err == nil {
		t.Fatal("ListTags on a non-MQ ARN should fail")
	}
}

// asAPIError is a local errors.As helper avoiding a direct errors import name
// clash with the driver package.
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

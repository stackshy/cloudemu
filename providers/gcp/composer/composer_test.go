package composer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithProjectID("proj")))
}

func createCfg() *cdriver.CreateEnvironmentConfig {
	return &cdriver.CreateEnvironmentConfig{
		Project:       "proj",
		Location:      "us-central1",
		EnvironmentID: "analytics",
		Labels:        map[string]string{"env": "test"},
		Config: cdriver.EnvironmentConfig{
			EnvironmentSize: "ENVIRONMENT_SIZE_SMALL",
			SoftwareConfig:  &cdriver.SoftwareConfig{ImageVersion: "composer-2.9.7-airflow-2.9.3"},
			NodeConfig:      &cdriver.NodeConfig{Network: "default", Subnetwork: "sub"},
			Other:           map[string]json.RawMessage{"privateEnvironmentConfig": json.RawMessage(`{"enablePrivateEnvironment":true}`)},
		},
	}
}

func TestCreateComputedFieldsDeterministic(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	env, op, err := m.CreateEnvironment(ctx, createCfg())
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	if env.State != cdriver.StateRunning {
		t.Fatalf("state = %q, want RUNNING", env.State)
	}

	if env.UUID == "" {
		t.Fatalf("uuid empty")
	}

	if env.CreateTime.IsZero() {
		t.Fatalf("createTime zero")
	}

	if env.Config.GkeCluster == "" || env.Config.DagGcsPrefix == "" || env.Config.AirflowURI == "" {
		t.Fatalf("computed outputs not derived: %+v", env.Config)
	}

	// Recreate in a fresh mock with the same identity -> identical computed
	// derivations (uuid excepted, which is random per create).
	m2 := newMock(t)

	env2, _, err := m2.CreateEnvironment(ctx, createCfg())
	if err != nil {
		t.Fatalf("second CreateEnvironment: %v", err)
	}

	if env.Config.GkeCluster != env2.Config.GkeCluster ||
		env.Config.DagGcsPrefix != env2.Config.DagGcsPrefix ||
		env.Config.AirflowURI != env2.Config.AirflowURI ||
		env.StorageBucket != env2.StorageBucket {
		t.Fatalf("computed derivations not deterministic across mocks")
	}
}

func TestCreateDefaultsImageVersion(t *testing.T) {
	m := newMock(t)

	cfg := createCfg()
	cfg.Config.SoftwareConfig = nil

	env, _, err := m.CreateEnvironment(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	if env.Config.SoftwareConfig == nil || env.Config.SoftwareConfig.ImageVersion != cdriver.DefaultImageVersion {
		t.Fatalf("default image version not applied: %+v", env.Config.SoftwareConfig)
	}
}

func TestCreateDuplicate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, _, err := m.CreateEnvironment(ctx, createCfg())
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	m := newMock(t)

	_, err := m.GetEnvironment(context.Background(), "proj", "us-central1", "ghost")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestGetReturnsDeepCopy(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := m.GetEnvironment(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Mutate the returned copy; a re-read must be unaffected.
	got.Labels["env"] = "mutated"
	got.Config.SoftwareConfig.ImageVersion = "mutated"
	got.Config.Other["privateEnvironmentConfig"] = json.RawMessage(`{"enablePrivateEnvironment":false}`)

	again, err := m.GetEnvironment(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("second get: %v", err)
	}

	if again.Labels["env"] != "test" || again.Config.SoftwareConfig.ImageVersion != "composer-2.9.7-airflow-2.9.3" {
		t.Fatalf("stored environment aliased by returned copy")
	}

	if string(again.Config.Other["privateEnvironmentConfig"]) != `{"enablePrivateEnvironment":true}` {
		t.Fatalf("stored Other block aliased by returned copy: %s", again.Config.Other["privateEnvironmentConfig"])
	}
}

func TestUpdateMaskSemantics(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("create: %v", err)
	}

	desired := &cdriver.EnvironmentConfig{
		EnvironmentSize: "ENVIRONMENT_SIZE_LARGE", // NOT in mask -> must be ignored
		SoftwareConfig:  &cdriver.SoftwareConfig{ImageVersion: "composer-2.9.8-airflow-2.9.3"},
	}

	env, op, err := m.UpdateEnvironment(ctx, "proj", "us-central1", "analytics",
		desired, nil, []string{"config.softwareconfig.imageversion"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if !op.Done {
		t.Fatalf("update operation not done")
	}

	if env.Config.SoftwareConfig.ImageVersion != "composer-2.9.8-airflow-2.9.3" {
		t.Fatalf("masked imageVersion not applied: %q", env.Config.SoftwareConfig.ImageVersion)
	}

	if env.Config.EnvironmentSize != "ENVIRONMENT_SIZE_SMALL" {
		t.Fatalf("unmasked environmentSize mutated: %q", env.Config.EnvironmentSize)
	}

	if env.State != cdriver.StateRunning {
		t.Fatalf("state after update = %q, want RUNNING", env.State)
	}
}

func TestUpdateOpaqueBlockViaMask(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("create: %v", err)
	}

	desired := &cdriver.EnvironmentConfig{
		Other: map[string]json.RawMessage{
			"privateEnvironmentConfig": json.RawMessage(`{"enablePrivateEnvironment":false}`),
		},
	}

	env, _, err := m.UpdateEnvironment(ctx, "proj", "us-central1", "analytics",
		desired, nil, []string{"config.privateEnvironmentConfig"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if string(env.Config.Other["privateEnvironmentConfig"]) != `{"enablePrivateEnvironment":false}` {
		t.Fatalf("masked opaque block not replaced: %s", env.Config.Other["privateEnvironmentConfig"])
	}
}

func TestListScoped(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("create: %v", err)
	}

	other := createCfg()
	other.Location = "europe-west1"
	other.EnvironmentID = "other"

	if _, _, err := m.CreateEnvironment(ctx, other); err != nil {
		t.Fatalf("create other: %v", err)
	}

	list, err := m.ListEnvironments(ctx, "proj", "us-central1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list) != 1 || list[0].EnvironmentID != "analytics" {
		t.Fatalf("list = %+v", list)
	}
}

func TestDeleteThenGet(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("create: %v", err)
	}

	op, err := m.DeleteEnvironment(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if !op.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := m.GetEnvironment(ctx, "proj", "us-central1", "analytics"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound after delete, got %v", err)
	}

	if _, err := m.DeleteEnvironment(ctx, "proj", "us-central1", "analytics"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound on second delete, got %v", err)
	}
}

func TestGetOperationUnknownIsDone(t *testing.T) {
	m := newMock(t)

	op, err := m.GetOperation(context.Background(), "projects/proj/locations/us-central1/operations/ghost")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}

	if !op.Done {
		t.Fatalf("unknown operation should report done")
	}
}

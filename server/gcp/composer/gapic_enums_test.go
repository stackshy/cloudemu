package composer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gapic "cloud.google.com/go/orchestration/airflow/service/apiv1"
	"cloud.google.com/go/orchestration/airflow/service/apiv1/servicepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newGAPIC(t *testing.T) *gapic.EnvironmentsClient {
	t.Helper()

	ts := httptest.NewServer(gcpserver.NewFromProvider(cloudemu.NewGCP()))
	t.Cleanup(ts.Close)

	c, err := gapic.NewEnvironmentsRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewEnvironmentsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func assertEnvEnums(t *testing.T, step string, env *servicepb.Environment) {
	t.Helper()

	cfg := env.GetConfig()

	if cfg.GetEnvironmentSize() != servicepb.EnvironmentConfig_ENVIRONMENT_SIZE_SMALL {
		t.Errorf("%s: environmentSize=%v want ENVIRONMENT_SIZE_SMALL", step, cfg.GetEnvironmentSize())
	}

	if cfg.GetResilienceMode() != servicepb.EnvironmentConfig_HIGH_RESILIENCE {
		t.Errorf("%s: resilienceMode=%v want HIGH_RESILIENCE", step, cfg.GetResilienceMode())
	}

	if env.GetState() != servicepb.Environment_RUNNING {
		t.Errorf("%s: state=%v want RUNNING", step, env.GetState())
	}
}

// TestGAPICEnvironmentNumericEnumsLifecycle is GCMP-01: the airflow
// service/apiv1 REST client sends environmentSize and resilienceMode as
// numbers. Create used to fail with "cannot unmarshal number into Go struct
// field EnvironmentConfig.config.environmentSize of type string". The update
// resends the fetched environment, so the output-only state goes back as a
// number too.
func TestGAPICEnvironmentNumericEnumsLifecycle(t *testing.T) {
	c := newGAPIC(t)
	ctx := context.Background()
	parent := "projects/mock-project/locations/us-central1"
	name := parent + "/environments/enum-env"

	op, err := c.CreateEnvironment(ctx, &servicepb.CreateEnvironmentRequest{
		Parent: parent,
		Environment: &servicepb.Environment{
			Name:   name,
			Labels: map[string]string{"env": "v1"},
			Config: &servicepb.EnvironmentConfig{
				EnvironmentSize: servicepb.EnvironmentConfig_ENVIRONMENT_SIZE_SMALL,
				ResilienceMode:  servicepb.EnvironmentConfig_HIGH_RESILIENCE,
				SoftwareConfig:  &servicepb.SoftwareConfig{ImageVersion: "composer-2.9.7-airflow-2.9.3"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateEnvironment Wait: %v", err)
	}

	assertEnvEnums(t, "create", created)

	got, err := c.GetEnvironment(ctx, &servicepb.GetEnvironmentRequest{Name: name})
	if err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}

	assertEnvEnums(t, "get", got)

	assertEnvListed(t, c, parent, name)

	got.Labels = map[string]string{"env": "v2"}

	uop, err := c.UpdateEnvironment(ctx, &servicepb.UpdateEnvironmentRequest{
		Name:        name,
		Environment: got,
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
	})
	if err != nil {
		t.Fatalf("UpdateEnvironment: %v", err)
	}

	updated, err := uop.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateEnvironment Wait: %v", err)
	}

	if updated.GetLabels()["env"] != "v2" {
		t.Errorf("update: labels=%v want env=v2", updated.GetLabels())
	}

	assertEnvEnums(t, "update", updated)

	dop, err := c.DeleteEnvironment(ctx, &servicepb.DeleteEnvironmentRequest{Name: name})
	if err != nil {
		t.Fatalf("DeleteEnvironment: %v", err)
	}

	if err := dop.Wait(ctx); err != nil {
		t.Fatalf("DeleteEnvironment Wait: %v", err)
	}

	_, err = c.GetEnvironment(ctx, &servicepb.GetEnvironmentRequest{Name: name})

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		t.Fatalf("GetEnvironment after delete: err=%v want 404", err)
	}
}

func assertEnvListed(t *testing.T, c *gapic.EnvironmentsClient, parent, name string) {
	t.Helper()

	it := c.ListEnvironments(context.Background(), &servicepb.ListEnvironmentsRequest{Parent: parent})

	for {
		env, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListEnvironments: %s not listed", name)
		}

		if err != nil {
			t.Fatalf("ListEnvironments: %v", err)
		}

		if env.GetName() == name {
			assertEnvEnums(t, "list", env)
			return
		}
	}
}

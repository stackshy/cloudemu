package cloudfunctions_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	functionsv1 "cloud.google.com/go/functions/apiv1"
	v1pb "cloud.google.com/go/functions/apiv1/functionspb"
	functionsv2 "cloud.google.com/go/functions/apiv2"
	v2pb "cloud.google.com/go/functions/apiv2/functionspb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const gapicParent = "projects/demo/locations/us-central1"

func gapicOptions(t *testing.T) []option.ClientOption {
	t.Helper()

	ts := httptest.NewServer(gcpserver.NewFromProvider(cloudemu.NewGCP()))
	t.Cleanup(ts.Close)

	return []option.ClientOption{
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	}
}

func assertNotFound(t *testing.T, step string, err error) {
	t.Helper()

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		t.Fatalf("%s: err=%v want 404", step, err)
	}
}

func assertGen2Enums(t *testing.T, step string, f *v2pb.Function, ingress v2pb.ServiceConfig_IngressSettings) {
	t.Helper()

	if f.GetEnvironment() != v2pb.Environment_GEN_2 {
		t.Errorf("%s: environment=%v want GEN_2", step, f.GetEnvironment())
	}

	if f.GetBuildConfig().GetDockerRegistry() != v2pb.BuildConfig_ARTIFACT_REGISTRY {
		t.Errorf("%s: dockerRegistry=%v want ARTIFACT_REGISTRY", step, f.GetBuildConfig().GetDockerRegistry())
	}

	if f.GetServiceConfig().GetIngressSettings() != ingress {
		t.Errorf("%s: ingressSettings=%v want %v", step, f.GetServiceConfig().GetIngressSettings(), ingress)
	}

	if f.GetState() != v2pb.Function_ACTIVE {
		t.Errorf("%s: state=%v want ACTIVE", step, f.GetState())
	}
}

// TestGAPICGen2NumericEnumsLifecycle is GCF-01: the functions/apiv2 REST
// client sends environment, dockerRegistry and ingressSettings as numbers.
// Create used to fail with "cannot unmarshal number into Go struct field". The
// update resends the fetched function, so the output-only state goes back as
// a number too.
func TestGAPICGen2NumericEnumsLifecycle(t *testing.T) {
	ctx := context.Background()

	c, err := functionsv2.NewFunctionRESTClient(ctx, gapicOptions(t)...)
	if err != nil {
		t.Fatalf("NewFunctionRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	op, err := c.CreateFunction(ctx, &v2pb.CreateFunctionRequest{
		Parent:     gapicParent,
		FunctionId: "enum-g2",
		Function: &v2pb.Function{
			Environment: v2pb.Environment_GEN_2,
			BuildConfig: &v2pb.BuildConfig{
				Runtime:        "go121",
				EntryPoint:     "Hello",
				DockerRegistry: v2pb.BuildConfig_ARTIFACT_REGISTRY,
				Source: &v2pb.Source{Source: &v2pb.Source_StorageSource{
					StorageSource: &v2pb.StorageSource{Bucket: "b", Object: "o.zip"},
				}},
			},
			ServiceConfig: &v2pb.ServiceConfig{
				IngressSettings:            v2pb.ServiceConfig_ALLOW_INTERNAL_ONLY,
				VpcConnectorEgressSettings: v2pb.ServiceConfig_ALL_TRAFFIC,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateFunction Wait: %v", err)
	}

	assertGen2Enums(t, "create", created, v2pb.ServiceConfig_ALLOW_INTERNAL_ONLY)

	name := created.GetName()

	got, err := c.GetFunction(ctx, &v2pb.GetFunctionRequest{Name: name})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}

	assertGen2Enums(t, "get", got, v2pb.ServiceConfig_ALLOW_INTERNAL_ONLY)

	assertGen2Listed(t, c, name)

	got.ServiceConfig.IngressSettings = v2pb.ServiceConfig_ALLOW_ALL

	uop, err := c.UpdateFunction(ctx, &v2pb.UpdateFunctionRequest{
		Function:   got,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"service_config.ingress_settings"}},
	})
	if err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}

	updated, err := uop.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateFunction Wait: %v", err)
	}

	assertGen2Enums(t, "update", updated, v2pb.ServiceConfig_ALLOW_ALL)

	dop, err := c.DeleteFunction(ctx, &v2pb.DeleteFunctionRequest{Name: name})
	if err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}

	if err := dop.Wait(ctx); err != nil {
		t.Fatalf("DeleteFunction Wait: %v", err)
	}

	_, err = c.GetFunction(ctx, &v2pb.GetFunctionRequest{Name: name})
	assertNotFound(t, "GetFunction after delete", err)
}

func assertGen2Listed(t *testing.T, c *functionsv2.FunctionClient, name string) {
	t.Helper()

	it := c.ListFunctions(context.Background(), &v2pb.ListFunctionsRequest{Parent: gapicParent})

	for {
		f, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListFunctions: %s not listed", name)
		}

		if err != nil {
			t.Fatalf("ListFunctions: %v", err)
		}

		if f.GetName() == name {
			assertGen2Enums(t, "list", f, v2pb.ServiceConfig_ALLOW_INTERNAL_ONLY)
			return
		}
	}
}

func assertGen1Enums(t *testing.T, step string, f *v1pb.CloudFunction, ingress v1pb.CloudFunction_IngressSettings) {
	t.Helper()

	if f.GetIngressSettings() != ingress {
		t.Errorf("%s: ingressSettings=%v want %v", step, f.GetIngressSettings(), ingress)
	}

	if f.GetDockerRegistry() != v1pb.CloudFunction_ARTIFACT_REGISTRY {
		t.Errorf("%s: dockerRegistry=%v want ARTIFACT_REGISTRY", step, f.GetDockerRegistry())
	}

	if f.GetStatus() != v1pb.CloudFunctionStatus_ACTIVE {
		t.Errorf("%s: status=%v want ACTIVE", step, f.GetStatus())
	}
}

// TestGAPICGen1NumericEnumsLifecycle is GCF-02: the functions/apiv1 REST
// client sends ingressSettings, dockerRegistry and, on an update of a fetched
// function, the output-only status as numbers. Each used to be a 400.
func TestGAPICGen1NumericEnumsLifecycle(t *testing.T) {
	ctx := context.Background()

	c, err := functionsv1.NewCloudFunctionsRESTClient(ctx, gapicOptions(t)...)
	if err != nil {
		t.Fatalf("NewCloudFunctionsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	name := gapicParent + "/functions/enum-g1"

	op, err := c.CreateFunction(ctx, &v1pb.CreateFunctionRequest{
		Location: gapicParent,
		Function: &v1pb.CloudFunction{
			Name:              name,
			Runtime:           "go121",
			EntryPoint:        "Hello",
			Trigger:           &v1pb.CloudFunction_HttpsTrigger{HttpsTrigger: &v1pb.HttpsTrigger{}},
			IngressSettings:   v1pb.CloudFunction_ALLOW_INTERNAL_ONLY,
			DockerRegistry:    v1pb.CloudFunction_ARTIFACT_REGISTRY,
			AvailableMemoryMb: 256,
		},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateFunction Wait: %v", err)
	}

	assertGen1Enums(t, "create", created, v1pb.CloudFunction_ALLOW_INTERNAL_ONLY)

	got, err := c.GetFunction(ctx, &v1pb.GetFunctionRequest{Name: name})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}

	assertGen1Enums(t, "get", got, v1pb.CloudFunction_ALLOW_INTERNAL_ONLY)

	assertGen1Listed(t, c, name)

	got.IngressSettings = v1pb.CloudFunction_ALLOW_ALL

	uop, err := c.UpdateFunction(ctx, &v1pb.UpdateFunctionRequest{
		Function:   got,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"ingress_settings"}},
	})
	if err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}

	updated, err := uop.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateFunction Wait: %v", err)
	}

	assertGen1Enums(t, "update", updated, v1pb.CloudFunction_ALLOW_ALL)

	dop, err := c.DeleteFunction(ctx, &v1pb.DeleteFunctionRequest{Name: name})
	if err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}

	if err := dop.Wait(ctx); err != nil {
		t.Fatalf("DeleteFunction Wait: %v", err)
	}

	_, err = c.GetFunction(ctx, &v1pb.GetFunctionRequest{Name: name})
	assertNotFound(t, "GetFunction after delete", err)
}

func assertGen1Listed(t *testing.T, c *functionsv1.CloudFunctionsClient, name string) {
	t.Helper()

	it := c.ListFunctions(context.Background(), &v1pb.ListFunctionsRequest{Parent: gapicParent})

	for {
		f, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListFunctions: %s not listed", name)
		}

		if err != nil {
			t.Fatalf("ListFunctions: %v", err)
		}

		if f.GetName() == name {
			assertGen1Enums(t, "list", f, v1pb.CloudFunction_ALLOW_INTERNAL_ONLY)
			return
		}
	}
}

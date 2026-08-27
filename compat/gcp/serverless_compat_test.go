package gcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	cf "google.golang.org/api/cloudfunctions/v1"
	cfv2 "google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestCloudFunctionsCompat drives a Cloud Functions control-plane lifecycle
// through the real google.golang.org/api/cloudfunctions/v1 client. Functions
// map onto the portable "serverless" driver, so operation names match AWS
// Lambda's in docs/coverage/coverage.json.
func TestCloudFunctionsCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{CloudFunctions: provider.CloudFunctions})
	ctx := context.Background()

	svcClient, err := cf.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("cloudfunctions service: %v", err)
	}

	const (
		svc     = "serverless"
		fnID    = "hello"
		runtime = "go121"
		memoryA = 128
		memoryB = 256
	)

	parent := "projects/" + compat.GCPProject + "/locations/us-central1"
	name := parent + "/functions/" + fnID

	sess.Op(svc, "CreateFunction", func() error {
		op, err := svcClient.Projects.Locations.Functions.Create(parent, &cf.CloudFunction{
			Name:              name,
			Runtime:           runtime,
			EntryPoint:        "Hello",
			AvailableMemoryMb: memoryA,
			Timeout:           "60s",
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("CreateFunction operation not done")
		}

		return nil
	})

	sess.Op(svc, "GetFunction", func() error {
		got, err := svcClient.Projects.Locations.Functions.Get(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Runtime != runtime {
			return fmt.Errorf("GetFunction runtime = %q, want %q", got.Runtime, runtime)
		}

		if !strings.HasSuffix(got.Name, "/functions/"+fnID) {
			return fmt.Errorf("GetFunction name = %q, want suffix /functions/%s", got.Name, fnID)
		}

		return nil
	})

	sess.Op(svc, "ListFunctions", func() error {
		list, err := svcClient.Projects.Locations.Functions.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Functions) != 1 {
			return fmt.Errorf("ListFunctions = %d functions, want 1", len(list.Functions))
		}

		return nil
	})

	sess.Op(svc, "UpdateFunction", func() error {
		op, err := svcClient.Projects.Locations.Functions.Patch(name, &cf.CloudFunction{
			Name:              name,
			Runtime:           runtime,
			EntryPoint:        "Hello",
			AvailableMemoryMb: memoryB,
			Timeout:           "120s",
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("UpdateFunction operation not done")
		}

		return nil
	})

	sess.Op(svc, "Invoke", func() error {
		provider.CloudFunctions.RegisterHandler(fnID, func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("got:" + string(payload)), nil
		})

		resp, err := svcClient.Projects.Locations.Functions.Call(name,
			&cf.CallFunctionRequest{Data: "hello"}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if resp.Result != "got:hello" {
			return fmt.Errorf("Invoke result = %q, want got:hello", resp.Result)
		}

		return nil
	})

	sess.Op(svc, "DeleteFunction", func() error {
		op, err := svcClient.Projects.Locations.Functions.Delete(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("DeleteFunction operation not done")
		}

		return nil
	})
}

// TestGen2GenerateDownloadURL proves the gen2 (v2) functions.generateDownloadUrl
// method returns a downloadUrl for an existing function, rather than 404
// ("unknown method"). generateUploadUrl already worked; download was the gap.
func TestGen2GenerateDownloadURL(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{CloudFunctions: provider.CloudFunctions})
	ctx := context.Background()

	svc, err := cfv2.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("cloudfunctions v2 service: %v", err)
	}

	parent := "projects/" + compat.GCPProject + "/locations/us-central1"
	name := parent + "/functions/g2dl"

	createOp, err := svc.Projects.Locations.Functions.Create(parent, &cfv2.Function{
		Environment: "GEN_2",
		BuildConfig: &cfv2.BuildConfig{
			Runtime:    "go121",
			EntryPoint: "Hello",
			Source: &cfv2.Source{
				StorageSource: &cfv2.StorageSource{Bucket: "b", Object: "o.zip"},
			},
		},
	}).FunctionId("g2dl").Context(ctx).Do()
	if err != nil {
		t.Fatalf("create gen2 function: %v", err)
	}

	if !createOp.Done {
		t.Fatal("create operation not done")
	}

	resp, err := svc.Projects.Locations.Functions.
		GenerateDownloadUrl(name, &cfv2.GenerateDownloadUrlRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("generateDownloadUrl: %v", err)
	}

	if resp.DownloadUrl == "" {
		t.Fatalf("generateDownloadUrl returned empty downloadUrl")
	}
}

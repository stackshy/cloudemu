package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsct "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	awsvpcl "github.com/aws/aws-sdk-go-v2/service/vpclattice"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestRecordsLatticeEvents pins that REST-JSON VPC Lattice calls (no
// X-Amz-Target or Action) show up in CloudTrail LookupEvents with the right
// event name, source and read-only flag.
func TestRecordsLatticeEvents(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)
	lattice := awsvpcl.NewFromConfig(cfg)

	if _, err = lattice.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("audited")}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if _, err = lattice.ListServices(ctx, &awsvpcl.ListServicesInput{}); err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	out, err := awsct.NewFromConfig(cfg).LookupEvents(ctx, &awsct.LookupEventsInput{})
	if err != nil {
		t.Fatalf("LookupEvents: %v", err)
	}

	sources := map[string]string{}
	readOnly := map[string]string{}

	for _, e := range out.Events {
		sources[aws.ToString(e.EventName)] = aws.ToString(e.EventSource)
		readOnly[aws.ToString(e.EventName)] = aws.ToString(e.ReadOnly)
	}

	if readOnly["CreateService"] != "false" || readOnly["ListServices"] != "true" {
		t.Fatalf("readOnly flags wrong: %v", readOnly)
	}

	for _, want := range []string{"CreateService", "ListServices"} {
		if src, ok := sources[want]; !ok || src != "vpc-lattice.amazonaws.com" {
			t.Fatalf("%s not recorded from vpc-lattice.amazonaws.com; events = %v", want, sources)
		}
	}
}

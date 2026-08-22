package dockerengine_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestComputeEC2ConsoleOutputE2E runs the exact flow a real user runs against AWS:
// launch an EC2 instance with the AWS SDK, passing a boot script via UserData, then
// read GetConsoleOutput and assert it contains the marker the boot script echoed —
// proving a real container actually ran the boot script — all against CloudEmu
// backed by a real Docker container (no cloud account). Then terminate and assert
// the console output is no longer available.
func TestComputeEC2ConsoleOutputE2E(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := dockerengine.NewCompute()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAWS(config.WithComputeEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const marker = "cloudemu-vm-marker-42"

	script := "#!/bin/sh\necho " + marker
	userData := base64.StdEncoding.EncodeToString([]byte(script))

	// 1. Launch the instance — exactly like `aws ec2 run-instances`.
	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-123"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
		UserData: aws.String(userData),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	if len(run.Instances) != 1 || run.Instances[0].InstanceId == nil {
		t.Fatalf("no instance returned: %+v", run.Instances)
	}

	instanceID := aws.ToString(run.Instances[0].InstanceId)

	// 2. Read the console output — the real container's boot-script output.
	con, err := client.GetConsoleOutput(ctx, &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	})
	if err != nil {
		t.Fatalf("GetConsoleOutput: %v", err)
	}

	// Real EC2 (and CloudEmu's wire layer) return the output base64-encoded.
	decoded, err := base64.StdEncoding.DecodeString(aws.ToString(con.Output))
	if err != nil {
		t.Fatalf("decode console output %q: %v", aws.ToString(con.Output), err)
	}

	if !strings.Contains(string(decoded), marker) {
		t.Fatalf("console output missing marker %q: got %q", marker, string(decoded))
	}

	// 3. Terminate the instance — the real container is torn down.
	if _, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	// 4. Console output for the torn-down instance no longer surfaces the marker.
	after, err := client.GetConsoleOutput(ctx, &ec2.GetConsoleOutputInput{
		InstanceId: aws.String(instanceID),
	})
	if err == nil {
		decodedAfter, _ := base64.StdEncoding.DecodeString(aws.ToString(after.Output))
		if strings.Contains(string(decodedAfter), marker) {
			t.Fatalf("console output still surfaces marker after termination: %q", string(decodedAfter))
		}
	}
}

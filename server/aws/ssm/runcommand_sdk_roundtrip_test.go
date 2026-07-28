package ssm_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newRunCommandClient(t *testing.T) (*awsssm.Client, *awsec2.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awsssm.NewFromConfig(cfg), awsec2.NewFromConfig(cfg)
}

// runInstances launches n instances and returns their ids, so Run Command has
// real targets rather than fabricated ones.
func runInstances(t *testing.T, ec2c *awsec2.Client, n int32) []string {
	t.Helper()

	out, err := ec2c.RunInstances(context.Background(), &awsec2.RunInstancesInput{
		ImageId: aws.String("ami-test"), InstanceType: "t3.micro",
		MinCount: aws.Int32(n), MaxCount: aws.Int32(n),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	ids := make([]string, 0, len(out.Instances))
	for i := range out.Instances {
		ids = append(ids, aws.ToString(out.Instances[i].InstanceId))
	}

	return ids
}

// A caller sends a bootstrap script then polls until the invocation reaches a
// terminal status and reads the response code. Nothing executes here — an
// emulated instance has no guest OS — so this pins the orchestration, not the
// script.
func TestRunCommandSendAndPoll(t *testing.T) {
	ctx := context.Background()
	c, ec2c := newRunCommandClient(t)
	ids := runInstances(t, ec2c, 1)

	sent, err := c.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds:  ids,
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters:   map[string][]string{"commands": {"echo hello"}},
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	if sent.Command == nil || aws.ToString(sent.Command.CommandId) == "" {
		t.Fatalf("SendCommand returned no command id: %+v", sent.Command)
	}

	inv, err := c.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{
		CommandId:  sent.Command.CommandId,
		InstanceId: aws.String(ids[0]),
	})
	if err != nil {
		t.Fatalf("GetCommandInvocation: %v", err)
	}

	// The caller's poll loop only exits on a terminal status; anything else
	// spins until its timeout.
	if inv.Status != ssmtypes.CommandInvocationStatusSuccess {
		t.Errorf("status = %q, want Success", inv.Status)
	}

	if inv.ResponseCode != 0 {
		t.Errorf("response code = %d, want 0", inv.ResponseCode)
	}
}

// One send targeting several instances must register an invocation for each —
// a caller polls per instance and would hang on the ones that were dropped.
func TestRunCommandRegistersEveryTargetInstance(t *testing.T) {
	ctx := context.Background()
	c, ec2c := newRunCommandClient(t)

	ids := runInstances(t, ec2c, 3)

	sent, err := c.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds:  ids,
		DocumentName: aws.String("AWS-RunShellScript"),
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	for _, id := range ids {
		if _, err := c.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{
			CommandId:  sent.Command.CommandId,
			InstanceId: aws.String(id),
		}); err != nil {
			t.Errorf("GetCommandInvocation(%s): %v", id, err)
		}
	}
}

// Polling a command that was never sent is a caller bug; answering Success
// would bury it.
func TestGetCommandInvocationUnknownFails(t *testing.T) {
	c, _ := newRunCommandClient(t)

	_, err := c.GetCommandInvocation(context.Background(), &awsssm.GetCommandInvocationInput{
		CommandId:  aws.String("never-sent"),
		InstanceId: aws.String("i-nope"),
	})
	if err == nil {
		t.Error("polling an unknown invocation should fail")
	}
}

func TestSendCommandRequiresInstances(t *testing.T) {
	c, _ := newRunCommandClient(t)

	_, err := c.SendCommand(context.Background(), &awsssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"),
	})
	if err == nil {
		t.Error("SendCommand with no instances should fail")
	}
}

// Real SSM answers InvalidInstanceId for a target that is not a managed
// instance. It is the most common Run Command failure during bring-up, so
// accepting any id would hide it until the caller runs for real.
func TestSendCommandRejectsUnknownInstance(t *testing.T) {
	c, _ := newRunCommandClient(t)

	_, err := c.SendCommand(context.Background(), &awsssm.SendCommandInput{
		InstanceIds:  []string{"i-doesnotexist"},
		DocumentName: aws.String("AWS-RunShellScript"),
	})
	if err == nil {
		t.Fatal("SendCommand to an unknown instance should fail")
	}

	if !strings.Contains(err.Error(), "InvalidInstanceId") {
		t.Errorf("error must name InvalidInstanceId, got: %v", err)
	}
}

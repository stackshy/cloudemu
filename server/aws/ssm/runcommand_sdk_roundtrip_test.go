package ssm_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newRunCommandClient(t *testing.T) *awsssm.Client {
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

	return awsssm.NewFromConfig(cfg)
}

// A caller sends a bootstrap script then polls until the invocation reaches a
// terminal status and reads the response code. Nothing executes here — an
// emulated instance has no guest OS — so this pins the orchestration, not the
// script.
func TestRunCommandSendAndPoll(t *testing.T) {
	ctx := context.Background()
	c := newRunCommandClient(t)

	sent, err := c.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds:  []string{"i-0123456789abcdef0"},
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
		InstanceId: aws.String("i-0123456789abcdef0"),
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
	c := newRunCommandClient(t)

	ids := []string{"i-aaa", "i-bbb", "i-ccc"}

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
	c := newRunCommandClient(t)

	_, err := c.GetCommandInvocation(context.Background(), &awsssm.GetCommandInvocationInput{
		CommandId:  aws.String("never-sent"),
		InstanceId: aws.String("i-nope"),
	})
	if err == nil {
		t.Error("polling an unknown invocation should fail")
	}
}

func TestSendCommandRequiresInstances(t *testing.T) {
	c := newRunCommandClient(t)

	_, err := c.SendCommand(context.Background(), &awsssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"),
	})
	if err == nil {
		t.Error("SendCommand with no instances should fail")
	}
}

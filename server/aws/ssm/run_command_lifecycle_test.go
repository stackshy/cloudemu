package ssm_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

type runCommandEnv struct {
	ssm *awsssm.Client
	ec2 *awsec2.Client
	s3  *awss3.Client
}

func newRunCommandEnv(t *testing.T, opts ...config.Option) runCommandEnv {
	t.Helper()

	cloud := cloudemu.NewAWS(opts...)
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return runCommandEnv{
		ssm: awsssm.NewFromConfig(cfg),
		ec2: awsec2.NewFromConfig(cfg),
		s3:  awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.UsePathStyle = true }),
	}
}

func shellParams(cmd string) map[string][]string {
	return map[string][]string{"commands": {cmd}}
}

// typedDocJSON declares one parameter of each checked kind.
const typedDocJSON = `{
  "schemaVersion": "2.2",
  "description": "typed parameters",
  "parameters": {
    "name":    {"type": "String", "allowedPattern": "^[a-z]+$"},
    "color":   {"type": "String", "default": "red", "allowedValues": ["red", "blue"]},
    "count":   {"type": "Integer", "default": 1},
    "enabled": {"type": "Boolean", "default": true},
    "files":   {"type": "StringList", "default": ["a"], "maxItems": 2},
    "labels":  {"type": "StringMap", "default": {}}
  },
  "mainSteps": [
    {"action": "aws:runShellScript", "name": "first", "inputs": {"runCommand": ["echo {{ name }}"]}},
    {"action": "aws:runShellScript", "name": "second", "inputs": {"runCommand": ["echo {{ color }}"]}}
  ]
}`

func TestSendCommandValidatesCatalogParameters(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 1)

	cases := map[string]map[string][]string{
		"missing required commands": nil,
		"undeclared parameter":      {"commands": {"ls"}, "bogus": {"x"}},
		"pattern mismatch":          {"commands": {"ls"}, "executionTimeout": {"999999"}},
		"two values for a String":   {"commands": {"ls"}, "workingDirectory": {"/a", "/b"}},
	}

	for name, params := range cases {
		_, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
			InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: params,
		})
		t.Run(name, func(t *testing.T) { wantAPIError(t, err, "InvalidParameters") })
	}

	if _, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{"commands": {"ls", "pwd"}, "executionTimeout": {"600"}},
	}); err != nil {
		t.Fatalf("valid send: %v", err)
	}
}

func TestSendCommandValidatesTypedParameters(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 1)

	if _, err := env.ssm.CreateDocument(ctx, &awsssm.CreateDocumentInput{
		Name: aws.String("typed-doc"), Content: aws.String(typedDocJSON),
	}); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	send := func(params map[string][]string) error {
		_, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
			InstanceIds: ids, DocumentName: aws.String("typed-doc"), Parameters: params,
		})

		return err
	}

	bad := map[string]map[string][]string{
		"required missing":     {"color": {"red"}},
		"pattern":              {"name": {"Bad1"}},
		"allowed values":       {"name": {"ok"}, "color": {"green"}},
		"integer":              {"name": {"ok"}, "count": {"many"}},
		"boolean":              {"name": {"ok"}, "enabled": {"yes"}},
		"string list maxItems": {"name": {"ok"}, "files": {"a", "b", "c"}},
		"string map":           {"name": {"ok"}, "labels": {"not-json"}},
	}

	for name, params := range bad {
		err := send(params)
		t.Run(name, func(t *testing.T) { wantAPIError(t, err, "InvalidParameters") })
	}

	good := map[string][]string{
		"name": {"ok"}, "color": {"blue"}, "count": {"7"}, "enabled": {"false"},
		"files": {"a", "b"}, "labels": {`{"k":"v"}`},
	}
	if err := send(good); err != nil {
		t.Fatalf("valid typed send: %v", err)
	}

	if err := send(map[string][]string{"name": {"ok"}}); err != nil {
		t.Fatalf("defaults should fill the optional parameters: %v", err)
	}
}

func TestSendCommandDocumentVersion(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 1)

	if _, err := env.ssm.CreateDocument(ctx, &awsssm.CreateDocumentInput{
		Name: aws.String("versioned"), Content: aws.String(docJSON),
	}); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	// Version 2 adds a required parameter; the default stays at 1.
	if _, err := env.ssm.UpdateDocument(ctx, &awsssm.UpdateDocumentInput{
		Name: aws.String("versioned"), Content: aws.String(typedDocJSON), DocumentVersion: aws.String("$LATEST"),
	}); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	send := func(version string, params map[string][]string) (*awsssm.SendCommandOutput, error) {
		in := &awsssm.SendCommandInput{InstanceIds: ids, DocumentName: aws.String("versioned"), Parameters: params}
		if version != "" {
			in.DocumentVersion = aws.String(version)
		}

		return env.ssm.SendCommand(ctx, in)
	}

	out, err := send("", nil)
	if err != nil {
		t.Fatalf("default version send: %v", err)
	}

	if got := aws.ToString(out.Command.DocumentVersion); got != "$DEFAULT" {
		t.Errorf("echoed DocumentVersion = %q, want $DEFAULT", got)
	}

	if _, err := send("1", map[string][]string{"msg": {"yo"}}); err != nil {
		t.Fatalf("version 1 send: %v", err)
	}

	_, err = send("$LATEST", map[string][]string{"msg": {"yo"}})
	wantAPIError(t, err, "InvalidParameters")

	out, err = send("2", map[string][]string{"name": {"ok"}})
	if err != nil {
		t.Fatalf("version 2 send: %v", err)
	}

	if got := aws.ToString(out.Command.DocumentVersion); got != "2" {
		t.Errorf("echoed DocumentVersion = %q, want 2", got)
	}

	_, err = send("9", nil)
	wantAPIError(t, err, "InvalidDocumentVersion")

	_, err = send("abc", nil)
	wantAPIError(t, err, "ValidationException")
}

func TestSendCommandEchoesRequest(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 2)

	out, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: shellParams("uptime"),
		Comment: aws.String("nightly"), MaxConcurrency: aws.String("1"), MaxErrors: aws.String("10%"),
		TimeoutSeconds: aws.Int32(120), OutputS3BucketName: aws.String("no-such-bucket"),
		OutputS3KeyPrefix: aws.String("runs"),
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	c := out.Command
	if aws.ToString(c.Comment) != "nightly" || aws.ToString(c.MaxConcurrency) != "1" ||
		aws.ToString(c.MaxErrors) != "10%" || c.TimeoutSeconds == nil || *c.TimeoutSeconds != 120 ||
		aws.ToString(c.OutputS3BucketName) != "no-such-bucket" || aws.ToString(c.OutputS3KeyPrefix) != "runs" ||
		c.TargetCount != 2 || c.RequestedDateTime == nil || c.ExpiresAfter == nil ||
		!c.ExpiresAfter.After(*c.RequestedDateTime) || len(c.Parameters["commands"]) != 1 {
		t.Fatalf("SendCommand echo = %+v", c)
	}

	if c.Status != ssmtypes.CommandStatusPending {
		t.Errorf("SendCommand status = %s, want Pending", c.Status)
	}

	for name, in := range map[string]*awsssm.SendCommandInput{
		"max concurrency": {MaxConcurrency: aws.String("0")},
		"max errors":      {MaxErrors: aws.String("x")},
		"timeout":         {TimeoutSeconds: aws.Int32(10)},
		"comment":         {Comment: aws.String(strings.Repeat("c", 101))},
		"instance id":     {InstanceIds: []string{"not-an-instance"}},
	} {
		if in.InstanceIds == nil {
			in.InstanceIds = ids
		}

		in.DocumentName = aws.String("AWS-RunShellScript")
		in.Parameters = shellParams("ls")

		_, err := env.ssm.SendCommand(ctx, in)
		t.Run(name, func(t *testing.T) { wantAPIError(t, err, "ValidationException") })
	}
}

func TestListCommandsAndInvocations(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 2)

	first, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: shellParams("one"),
	})
	if err != nil {
		t.Fatalf("send first: %v", err)
	}

	if _, err := env.ssm.CreateDocument(ctx, &awsssm.CreateDocumentInput{
		Name: aws.String("typed-doc"), Content: aws.String(typedDocJSON),
	}); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	second, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids[:1], DocumentName: aws.String("typed-doc"), Parameters: map[string][]string{"name": {"ok"}},
	})
	if err != nil {
		t.Fatalf("send second: %v", err)
	}

	all, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{})
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}

	if len(all.Commands) != 2 {
		t.Fatalf("ListCommands = %d commands, want 2", len(all.Commands))
	}

	for _, c := range all.Commands {
		if c.Status != ssmtypes.CommandStatusSuccess || aws.ToString(c.StatusDetails) != "Success" ||
			c.CompletedCount != c.TargetCount {
			t.Errorf("listed command = %+v, want settled Success", c)
		}
	}

	byID, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{CommandId: second.Command.CommandId})
	if err != nil || len(byID.Commands) != 1 || aws.ToString(byID.Commands[0].DocumentName) != "typed-doc" {
		t.Fatalf("ListCommands by id = %+v, %v", byID, err)
	}

	byInstance, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{InstanceId: aws.String(ids[1])})
	if err != nil || len(byInstance.Commands) != 1 ||
		aws.ToString(byInstance.Commands[0].CommandId) != aws.ToString(first.Command.CommandId) {
		t.Fatalf("ListCommands by instance = %+v, %v", byInstance, err)
	}

	byDoc, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{Filters: []ssmtypes.CommandFilter{
		{Key: ssmtypes.CommandFilterKeyDocumentName, Value: aws.String("AWS-RunShellScript")},
		{Key: ssmtypes.CommandFilterKeyExecutionStage, Value: aws.String("Complete")},
	}})
	if err != nil || len(byDoc.Commands) != 1 {
		t.Fatalf("ListCommands by document = %+v, %v", byDoc, err)
	}

	page, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{MaxResults: aws.Int32(1)})
	if err != nil || len(page.Commands) != 1 || page.NextToken == nil {
		t.Fatalf("ListCommands page 1 = %+v, %v", page, err)
	}

	page2, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{MaxResults: aws.Int32(1), NextToken: page.NextToken})
	if err != nil || len(page2.Commands) != 1 || page2.NextToken != nil ||
		aws.ToString(page2.Commands[0].CommandId) == aws.ToString(page.Commands[0].CommandId) {
		t.Fatalf("ListCommands page 2 = %+v, %v", page2, err)
	}

	_, err = env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{Filters: []ssmtypes.CommandFilter{
		{Key: "Nope", Value: aws.String("x")},
	}})
	wantAPIError(t, err, "InvalidFilterKey")

	_, err = env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{NextToken: aws.String("garbage!")})
	wantAPIError(t, err, "InvalidNextToken")

	invs, err := env.ssm.ListCommandInvocations(ctx, &awsssm.ListCommandInvocationsInput{
		CommandId: first.Command.CommandId,
	})
	if err != nil || len(invs.CommandInvocations) != 2 {
		t.Fatalf("ListCommandInvocations = %+v, %v", invs, err)
	}

	for _, inv := range invs.CommandInvocations {
		if inv.Status != ssmtypes.CommandInvocationStatusSuccess || len(inv.CommandPlugins) != 0 {
			t.Errorf("invocation without details = %+v", inv)
		}
	}

	detailed, err := env.ssm.ListCommandInvocations(ctx, &awsssm.ListCommandInvocationsInput{
		CommandId: second.Command.CommandId, Details: true,
	})
	if err != nil || len(detailed.CommandInvocations) != 1 {
		t.Fatalf("ListCommandInvocations details = %+v, %v", detailed, err)
	}

	plugins := detailed.CommandInvocations[0].CommandPlugins
	if len(plugins) != 2 || aws.ToString(plugins[0].Name) != "first" || aws.ToString(plugins[1].Name) != "second" ||
		plugins[0].Status != ssmtypes.CommandPluginStatusSuccess || plugins[0].ResponseCode != 0 {
		t.Fatalf("command plugins = %+v", plugins)
	}

	_, err = env.ssm.ListCommandInvocations(ctx, &awsssm.ListCommandInvocationsInput{Filters: []ssmtypes.CommandFilter{
		{Key: ssmtypes.CommandFilterKeyExecutionStage, Value: aws.String("Complete")},
	}})
	wantAPIError(t, err, "InvalidFilterKey")

	step, err := env.ssm.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{
		CommandId: second.Command.CommandId, InstanceId: aws.String(ids[0]), PluginName: aws.String("second"),
	})
	if err != nil || aws.ToString(step.PluginName) != "second" || step.Status != ssmtypes.CommandInvocationStatusSuccess {
		t.Fatalf("GetCommandInvocation plugin = %+v, %v", step, err)
	}

	_, err = env.ssm.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{
		CommandId: second.Command.CommandId, InstanceId: aws.String(ids[0]), PluginName: aws.String("third"),
	})
	wantAPIError(t, err, "InvalidPluginName")
}

// With async settle a command moves Pending -> InProgress -> Success, and
// every read reports the same state.
func TestRunCommandAsyncLifecycle(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	env := newRunCommandEnv(t, config.WithClock(fc), config.WithAsyncSettle())
	ids := runInstances(t, env.ec2, 1)
	fc.Advance(time.Minute)

	out, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: shellParams("sleep 1"),
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	id := out.Command.CommandId

	observe := func(wantCmd ssmtypes.CommandStatus, wantInv ssmtypes.CommandInvocationStatus) *awsssm.GetCommandInvocationOutput {
		t.Helper()

		inv, err := env.ssm.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{CommandId: id, InstanceId: aws.String(ids[0])})
		if err != nil || inv.Status != wantInv {
			t.Fatalf("GetCommandInvocation = %+v, %v, want %s", inv, err, wantInv)
		}

		listed, err := env.ssm.ListCommandInvocations(ctx, &awsssm.ListCommandInvocationsInput{CommandId: id})
		if err != nil || len(listed.CommandInvocations) != 1 || listed.CommandInvocations[0].Status != wantInv {
			t.Fatalf("ListCommandInvocations = %+v, %v, want %s", listed, err, wantInv)
		}

		cmds, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{CommandId: id})
		if err != nil || len(cmds.Commands) != 1 || cmds.Commands[0].Status != wantCmd {
			t.Fatalf("ListCommands = %+v, %v, want %s", cmds, err, wantCmd)
		}

		return inv
	}

	inv := observe(ssmtypes.CommandStatusPending, ssmtypes.CommandInvocationStatusPending)
	if inv.ResponseCode != -1 || aws.ToString(inv.ExecutionStartDateTime) != "" {
		t.Errorf("pending invocation = %+v", inv)
	}

	fc.Advance(1500 * time.Millisecond)

	inv = observe(ssmtypes.CommandStatusInProgress, ssmtypes.CommandInvocationStatusInProgress)
	if inv.ResponseCode != -1 || aws.ToString(inv.ExecutionStartDateTime) == "" {
		t.Errorf("in-progress invocation = %+v", inv)
	}

	fc.Advance(time.Minute)

	inv = observe(ssmtypes.CommandStatusSuccess, ssmtypes.CommandInvocationStatusSuccess)
	if inv.ResponseCode != 0 || aws.ToString(inv.ExecutionEndDateTime) == "" || aws.ToString(inv.ExecutionElapsedTime) == "" {
		t.Errorf("finished invocation = %+v", inv)
	}
}

func TestCancelCommand(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	env := newRunCommandEnv(t, config.WithClock(fc), config.WithAsyncSettle())
	ids := runInstances(t, env.ec2, 2)
	fc.Advance(time.Minute)

	send := func() *string {
		out, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
			InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: shellParams("sleep 60"),
		})
		if err != nil {
			t.Fatalf("SendCommand: %v", err)
		}

		return out.Command.CommandId
	}

	running := send()
	fc.Advance(1500 * time.Millisecond)

	_, err := env.ssm.CancelCommand(ctx, &awsssm.CancelCommandInput{CommandId: aws.String("11111111-2222-3333-4444-555555555555")})
	wantAPIError(t, err, "InvalidCommandId")

	_, err = env.ssm.CancelCommand(ctx, &awsssm.CancelCommandInput{CommandId: running, InstanceIds: []string{"i-0123456789abcdef0"}})
	wantAPIError(t, err, "InvalidInstanceId")

	if _, err := env.ssm.CancelCommand(ctx, &awsssm.CancelCommandInput{CommandId: running}); err != nil {
		t.Fatalf("CancelCommand: %v", err)
	}

	fc.Advance(time.Minute)

	cmds, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{CommandId: running})
	if err != nil || cmds.Commands[0].Status != ssmtypes.CommandStatusCancelled || cmds.Commands[0].CompletedCount != 2 {
		t.Fatalf("cancelled command = %+v, %v", cmds, err)
	}

	for _, id := range ids {
		inv, err := env.ssm.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{CommandId: running, InstanceId: aws.String(id)})
		if err != nil || inv.Status != ssmtypes.CommandInvocationStatusCancelled {
			t.Fatalf("cancelled invocation = %+v, %v", inv, err)
		}
	}

	// A finished command is left as it is.
	done := send()
	fc.Advance(time.Minute)

	if _, err := env.ssm.CancelCommand(ctx, &awsssm.CancelCommandInput{CommandId: done}); err != nil {
		t.Fatalf("CancelCommand on finished command: %v", err)
	}

	cmds, err = env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{CommandId: done})
	if err != nil || cmds.Commands[0].Status != ssmtypes.CommandStatusSuccess {
		t.Fatalf("finished command after cancel = %+v, %v", cmds, err)
	}
}

// executionTimeout shorter than the run makes the invocation time out.
func TestRunCommandExecutionTimeout(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	env := newRunCommandEnv(t, config.WithClock(fc), config.WithAsyncSettle())
	ids := runInstances(t, env.ec2, 1)
	fc.Advance(time.Minute)

	out, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{"commands": {"sleep 100"}, "executionTimeout": {"1"}},
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	fc.Advance(time.Minute)

	inv, err := env.ssm.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{
		CommandId: out.Command.CommandId, InstanceId: aws.String(ids[0]),
	})
	if err != nil || inv.Status != ssmtypes.CommandInvocationStatusTimedOut ||
		aws.ToString(inv.StatusDetails) != "ExecutionTimedOut" {
		t.Fatalf("timed out invocation = %+v, %v", inv, err)
	}

	cmds, err := env.ssm.ListCommands(ctx, &awsssm.ListCommandsInput{CommandId: out.Command.CommandId})
	if err != nil || cmds.Commands[0].Status != ssmtypes.CommandStatusTimedOut || cmds.Commands[0].ErrorCount != 1 {
		t.Fatalf("timed out command = %+v, %v", cmds, err)
	}
}

func TestSendCommandWritesOutputToS3(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 1)

	if _, err := env.s3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("run-output")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	out, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: shellParams("hostname"),
		OutputS3BucketName: aws.String("run-output"), OutputS3KeyPrefix: aws.String("logs"),
	})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	id := aws.ToString(out.Command.CommandId)
	wantKey := "logs/" + id + "/" + ids[0] + "/awsrunShellScript/0.awsrunShellScript/stdout"

	inv, err := env.ssm.GetCommandInvocation(ctx, &awsssm.GetCommandInvocationInput{
		CommandId: out.Command.CommandId, InstanceId: aws.String(ids[0]),
	})
	if err != nil || !strings.HasSuffix(aws.ToString(inv.StandardOutputUrl), "/run-output/"+wantKey) {
		t.Fatalf("StandardOutputUrl = %q, %v, want suffix %s", aws.ToString(inv.StandardOutputUrl), err, wantKey)
	}

	objs, err := env.s3.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("run-output"), Prefix: aws.String("logs/")})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}

	found := false
	for _, o := range objs.Contents {
		found = found || aws.ToString(o.Key) == wantKey
	}

	if !found {
		t.Fatalf("output object %s not written; bucket has %+v", wantKey, objs.Contents)
	}
}

func TestSendCommandRejectsStoppedInstance(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 1)

	if _, err := env.ec2.StopInstances(ctx, &awsec2.StopInstancesInput{InstanceIds: ids}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	_, err := env.ssm.SendCommand(ctx, &awsssm.SendCommandInput{
		InstanceIds: ids, DocumentName: aws.String("AWS-RunShellScript"), Parameters: shellParams("ls"),
	})
	wantAPIError(t, err, "InvalidInstanceId")
}

func TestDescribeInstanceInformation(t *testing.T) {
	ctx := context.Background()
	env := newRunCommandEnv(t)
	ids := runInstances(t, env.ec2, 2)

	if _, err := env.ec2.StopInstances(ctx, &awsec2.StopInstancesInput{InstanceIds: ids[1:]}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	out, err := env.ssm.DescribeInstanceInformation(ctx, &awsssm.DescribeInstanceInformationInput{})
	if err != nil || len(out.InstanceInformationList) != 2 {
		t.Fatalf("DescribeInstanceInformation = %+v, %v", out, err)
	}

	status := map[string]ssmtypes.PingStatus{}
	for _, info := range out.InstanceInformationList {
		status[aws.ToString(info.InstanceId)] = info.PingStatus

		if info.ResourceType != ssmtypes.ResourceTypeEc2Instance || info.PlatformType != ssmtypes.PlatformTypeLinux ||
			aws.ToString(info.AgentVersion) == "" {
			t.Errorf("instance information = %+v", info)
		}
	}

	if status[ids[0]] != ssmtypes.PingStatusOnline || status[ids[1]] != ssmtypes.PingStatusConnectionLost {
		t.Fatalf("ping status = %v", status)
	}

	online, err := env.ssm.DescribeInstanceInformation(ctx, &awsssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{{Key: aws.String("PingStatus"), Values: []string{"Online"}}},
	})
	if err != nil || len(online.InstanceInformationList) != 1 || aws.ToString(online.InstanceInformationList[0].InstanceId) != ids[0] {
		t.Fatalf("PingStatus filter = %+v, %v", online, err)
	}

	_, err = env.ssm.DescribeInstanceInformation(ctx, &awsssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{{Key: aws.String("Bogus"), Values: []string{"x"}}},
	})
	wantAPIError(t, err, "InvalidFilterKey")
}

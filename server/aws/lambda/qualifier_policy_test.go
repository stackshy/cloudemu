package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
)

// errorCode returns the AWS error code from a smithy API error, or "" if err is
// not an API error.
func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}

// TestSDKGetFunctionConfigurationQualifier is a regression guard for the audit's
// F1: GetFunction/GetFunctionConfiguration ignored the Qualifier and always
// returned $LATEST. A published version (or alias) must report its own
// Version/Timeout/CodeSha256 and a qualified ARN.
func TestSDKGetFunctionConfigurationQualifier(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("cfgqual"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Timeout:      aws.Int32(3),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("v1-code")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	pub, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("cfgqual")})
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if aws.ToString(pub.Version) != "1" {
		t.Fatalf("published version = %q, want 1", aws.ToString(pub.Version))
	}

	v1Sha := aws.ToString(pub.CodeSha256)

	// Mutate $LATEST: new code (new CodeSha256) and a new Timeout.
	if _, err = client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String("cfgqual"), ZipFile: []byte("v2-code-different"),
	}); err != nil {
		t.Fatalf("UpdateFunctionCode: %v", err)
	}

	if _, err = client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("cfgqual"), Timeout: aws.Int32(99),
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}

	// GetFunctionConfiguration(Qualifier="1") must return v1's snapshot.
	v1cfg, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("cfgqual"), Qualifier: aws.String("1"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration(Qualifier=1): %v", err)
	}

	if got := aws.ToString(v1cfg.Version); got != "1" {
		t.Fatalf("v1 Version = %q, want 1", got)
	}

	if got := aws.ToInt32(v1cfg.Timeout); got != 3 {
		t.Fatalf("v1 Timeout = %d, want 3 (the published snapshot, not $LATEST's 99)", got)
	}

	if got := aws.ToString(v1cfg.CodeSha256); got != v1Sha {
		t.Fatalf("v1 CodeSha256 = %q, want the published %q", got, v1Sha)
	}

	if got := aws.ToString(v1cfg.FunctionArn); !strings.HasSuffix(got, ":function:cfgqual:1") {
		t.Fatalf("v1 FunctionArn = %q, want a :1-qualified ARN", got)
	}

	// Unqualified GetFunctionConfiguration is $LATEST — unchanged.
	latest, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("cfgqual"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration($LATEST): %v", err)
	}

	if got := aws.ToString(latest.Version); got != "$LATEST" {
		t.Fatalf("$LATEST Version = %q, want $LATEST", got)
	}

	if got := aws.ToInt32(latest.Timeout); got != 99 {
		t.Fatalf("$LATEST Timeout = %d, want 99", got)
	}

	if aws.ToString(latest.CodeSha256) == v1Sha {
		t.Fatal("$LATEST CodeSha256 equals v1's, want the updated code's hash")
	}

	if got := aws.ToString(latest.FunctionArn); strings.HasSuffix(got, ":1") {
		t.Fatalf("$LATEST FunctionArn = %q, want no version suffix", got)
	}

	// GetFunction(Qualifier="1") resolves the same way.
	getV1, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("cfgqual"), Qualifier: aws.String("1"),
	})
	if err != nil {
		t.Fatalf("GetFunction(Qualifier=1): %v", err)
	}

	if got := aws.ToString(getV1.Configuration.Version); got != "1" {
		t.Fatalf("GetFunction v1 Version = %q, want 1", got)
	}

	// A bad qualifier is ResourceNotFoundException.
	if _, err = client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("cfgqual"), Qualifier: aws.String("99"),
	}); errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("GetFunctionConfiguration(Qualifier=99) error = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKGetFunctionConfigurationAliasQualifier covers an alias Qualifier: it
// resolves to the target version's config but keeps the alias-qualified ARN.
func TestSDKGetFunctionConfigurationAliasQualifier(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("aliascfg"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Timeout:      aws.Int32(7),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("alias-code")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String("aliascfg"),
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName: aws.String("aliascfg"), Name: aws.String("prod"), FunctionVersion: aws.String("1"),
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	// Move $LATEST's Timeout so the alias (-> v1) must not read $LATEST.
	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("aliascfg"), Timeout: aws.Int32(88),
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}

	cfg, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("aliascfg"), Qualifier: aws.String("prod"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration(Qualifier=prod): %v", err)
	}

	if got := aws.ToString(cfg.Version); got != "1" {
		t.Fatalf("alias Version = %q, want 1 (the target version)", got)
	}

	if got := aws.ToInt32(cfg.Timeout); got != 7 {
		t.Fatalf("alias Timeout = %d, want 7 (v1's snapshot)", got)
	}

	if got := aws.ToString(cfg.FunctionArn); !strings.HasSuffix(got, ":function:aliascfg:prod") {
		t.Fatalf("alias FunctionArn = %q, want a :prod-qualified ARN", got)
	}
}

// TestSDKCreateEventSourceMappingNonexistentFunction is a regression guard for
// F2: creating an ESM whose target function does not exist must fail with
// ResourceNotFoundException instead of silently succeeding.
func TestSDKCreateEventSourceMappingNonexistentFunction(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName:   aws.String("does-not-exist"),
		EventSourceArn: aws.String("arn:aws:sqs:us-east-1:000000000000:ghost-queue"),
	})
	if errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("CreateEventSourceMapping(nonexistent) error = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKAddPermissionQualifierScoped is a regression guard for F3: a grant with
// a Qualifier must land on that alias/version's policy only, separate from the
// unqualified ($LATEST) policy.
func TestSDKAddPermissionQualifierScoped(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "permqual")

	if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String("permqual"),
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName: aws.String("permqual"), Name: aws.String("live"), FunctionVersion: aws.String("1"),
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	if _, err := client.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("permqual"),
		Qualifier:    aws.String("live"),
		StatementId:  aws.String("acct-invoke"),
		Action:       aws.String("lambda:InvokeFunction"),
		Principal:    aws.String("123456789012"),
	}); err != nil {
		t.Fatalf("AddPermission(Qualifier=live): %v", err)
	}

	// The alias policy carries the statement.
	livePolicy, err := client.GetPolicy(ctx, &awslambda.GetPolicyInput{
		FunctionName: aws.String("permqual"), Qualifier: aws.String("live"),
	})
	if err != nil {
		t.Fatalf("GetPolicy(Qualifier=live): %v", err)
	}

	if !strings.Contains(aws.ToString(livePolicy.Policy), `"Sid":"acct-invoke"`) {
		t.Fatalf("alias policy missing statement: %s", aws.ToString(livePolicy.Policy))
	}

	// The unqualified ($LATEST) policy does not — a separate policy per qualifier.
	if _, err := client.GetPolicy(ctx, &awslambda.GetPolicyInput{
		FunctionName: aws.String("permqual"),
	}); errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("GetPolicy(unqualified) error = %v, want ResourceNotFoundException (no unqualified policy)", err)
	}

	// RemovePermission is also qualifier-scoped.
	if _, err := client.RemovePermission(ctx, &awslambda.RemovePermissionInput{
		FunctionName: aws.String("permqual"), Qualifier: aws.String("live"), StatementId: aws.String("acct-invoke"),
	}); err != nil {
		t.Fatalf("RemovePermission(Qualifier=live): %v", err)
	}

	if _, err := client.GetPolicy(ctx, &awslambda.GetPolicyInput{
		FunctionName: aws.String("permqual"), Qualifier: aws.String("live"),
	}); errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("GetPolicy(Qualifier=live) after remove error = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKGetPolicyPrincipalShapes is a regression guard for F4: the Principal is
// rendered per its type — Service for a service domain, AWS root ARN for an
// account id, and a bare "*" wildcard.
func TestSDKGetPolicyPrincipalShapes(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "principals")

	grants := []struct {
		sid       string
		principal string
	}{
		{"svc", "s3.amazonaws.com"},
		{"acct", "123456789012"},
		{"any", "*"},
	}
	for _, g := range grants {
		if _, err := client.AddPermission(ctx, &awslambda.AddPermissionInput{
			FunctionName: aws.String("principals"),
			StatementId:  aws.String(g.sid),
			Action:       aws.String("lambda:InvokeFunction"),
			Principal:    aws.String(g.principal),
		}); err != nil {
			t.Fatalf("AddPermission(%s): %v", g.sid, err)
		}
	}

	out, err := client.GetPolicy(ctx, &awslambda.GetPolicyInput{FunctionName: aws.String("principals")})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	policy := aws.ToString(out.Policy)

	for _, want := range []string{
		`"Principal":{"Service":"s3.amazonaws.com"}`,
		`"Principal":{"AWS":"arn:aws:iam::123456789012:root"}`,
		`"Principal":"*"`,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %s\ngot: %s", want, policy)
		}
	}
}

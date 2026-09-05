package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// publishV1 creates function name and publishes version 1, returning the
// unqualified function ARN (the $LATEST ARN, without a :version suffix).
func publishV1(t *testing.T, client *awslambda.Client, name string) string {
	t.Helper()

	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("v1-code")},
	}); err != nil {
		t.Fatalf("CreateFunction(%s): %v", name, err)
	}

	if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String(name),
	}); err != nil {
		t.Fatalf("PublishVersion(%s): %v", name, err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration(%s): %v", name, err)
	}

	return aws.ToString(out.FunctionArn)
}

// TestSDKGetFunctionConfigurationQualifiedForms is a regression guard for the
// audit finding that the FunctionName path segment accepted only a bare name:
// Terraform's aws_lambda_function{publish=true} update-and-wait flow does a
// GetFunctionConfiguration on the fully-qualified ARN (arn:...:function:foo:2)
// after publishing, which returned ResourceNotFoundException. Real Lambda
// accepts a bare name, name:qualifier, an unqualified ARN, and a qualified ARN.
func TestSDKGetFunctionConfigurationQualifiedForms(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	arn := publishV1(t, client, "foo")

	cases := []struct {
		ref     string
		version string
	}{
		{"foo", "$LATEST"},
		{"foo:1", "1"},
		{arn, "$LATEST"},
		{arn + ":1", "1"},
	}

	for _, tc := range cases {
		out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
			FunctionName: aws.String(tc.ref),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration(FunctionName=%q): %v", tc.ref, err)
		}

		if got := aws.ToString(out.Version); got != tc.version {
			t.Errorf("GetFunctionConfiguration(FunctionName=%q) Version = %q, want %q", tc.ref, got, tc.version)
		}
	}
}

// TestSDKGetFunctionConfigurationMissingQualifier verifies a FunctionName that
// embeds a non-existent version qualifier is a ResourceNotFoundException, not a
// spurious success.
func TestSDKGetFunctionConfigurationMissingQualifier(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	_, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:99"),
	})
	if errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("GetFunctionConfiguration(foo:99) error = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKGetFunctionConfigurationQualifierConflict verifies that a qualifier
// embedded in the FunctionName that disagrees with the explicit Qualifier
// parameter is a ValidationException, while agreeing qualifiers resolve normally.
func TestSDKGetFunctionConfigurationQualifierConflict(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	_, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:1"),
		Qualifier:    aws.String("2"),
	})
	if errorCode(err) != "ValidationException" {
		t.Fatalf("GetFunctionConfiguration(foo:1, Qualifier=2) error = %v, want ValidationException", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:1"),
		Qualifier:    aws.String("1"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration(foo:1, Qualifier=1): %v", err)
	}

	if got := aws.ToString(out.Version); got != "1" {
		t.Errorf("GetFunctionConfiguration(foo:1, Qualifier=1) Version = %q, want %q", got, "1")
	}
}

// TestSDKQualifiedFormsAcrossOps spot-checks that Invoke and UpdateFunctionCode
// also accept the qualified/ARN FunctionName forms. (DeleteFunction's qualified
// form is covered by TestSDKDeleteFunctionQualifierScoped, which asserts the
// scoped effect rather than just that the call succeeds.)
func TestSDKQualifiedFormsAcrossOps(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	arn := publishV1(t, client, "foo")

	// Invoke against the version-qualified ARN.
	if _, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String(arn + ":1"),
		Payload:      []byte(`{}`),
	}); err != nil {
		t.Fatalf("Invoke(%s:1): %v", arn, err)
	}

	// UpdateFunctionCode against the unqualified ARN (operates on $LATEST).
	if _, err := client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(arn),
		ZipFile:      []byte("v2-code"),
	}); err != nil {
		t.Fatalf("UpdateFunctionCode(%s): %v", arn, err)
	}
}

// listVersionNumbers returns the set of published version identifiers ($LATEST
// plus every numeric version) reported by ListVersionsByFunction.
func listVersionNumbers(t *testing.T, client *awslambda.Client, name string) map[string]bool {
	t.Helper()

	out, err := client.ListVersionsByFunction(context.Background(), &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("ListVersionsByFunction(%s): %v", name, err)
	}

	got := make(map[string]bool, len(out.Versions))
	for _, v := range out.Versions {
		got[aws.ToString(v.Version)] = true
	}

	return got
}

// publishV2 updates the function code and publishes a second version, so the
// function has $LATEST, 1 and 2. It assumes publishV1 has already run.
func publishV2(t *testing.T, client *awslambda.Client, name string) {
	t.Helper()

	ctx := context.Background()

	if _, err := client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(name),
		ZipFile:      []byte("v2-code"),
	}); err != nil {
		t.Fatalf("UpdateFunctionCode(%s): %v", name, err)
	}

	if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String(name),
	}); err != nil {
		t.Fatalf("PublishVersion(%s) v2: %v", name, err)
	}
}

// TestSDKDeleteFunctionQualifierScoped is the data-loss regression guard: a
// DeleteFunction with a version qualifier (name:qualifier or ?Qualifier=) must
// delete ONLY that version, leaving $LATEST and the other versions intact. Real
// Lambda: "To delete a specific function version, use the Qualifier parameter.
// Otherwise, all versions and aliases are deleted." Before the fix the qualifier
// was ignored and the whole function was deleted.
func TestSDKDeleteFunctionQualifierScoped(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")
	publishV2(t, client, "foo")

	if got := listVersionNumbers(t, client, "foo"); !got["$LATEST"] || !got["1"] || !got["2"] {
		t.Fatalf("precondition: versions = %v, want $LATEST,1,2", got)
	}

	// Delete only version 1 via the name:qualifier short form.
	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo:1"),
	}); err != nil {
		t.Fatalf("DeleteFunction(foo:1): %v", err)
	}

	got := listVersionNumbers(t, client, "foo")
	if got["1"] {
		t.Errorf("DeleteFunction(foo:1) left version 1 present; versions = %v", got)
	}

	if !got["$LATEST"] || !got["2"] {
		t.Errorf("DeleteFunction(foo:1) over-deleted; versions = %v, want $LATEST and 2 to remain", got)
	}

	// $LATEST must still be resolvable (the function itself survives).
	if _, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo"),
	}); err != nil {
		t.Errorf("GetFunctionConfiguration(foo) after scoped delete: %v", err)
	}

	// Version 2 must still be resolvable via its qualifier.
	if _, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:2"),
	}); err != nil {
		t.Errorf("GetFunctionConfiguration(foo:2) after scoped delete: %v", err)
	}
}

// TestSDKDeleteFunctionQualifierParam verifies the scoped delete also works via
// the explicit ?Qualifier= parameter (not only the name:qualifier form).
func TestSDKDeleteFunctionQualifierParam(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")
	publishV2(t, client, "foo")

	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo"),
		Qualifier:    aws.String("2"),
	}); err != nil {
		t.Fatalf("DeleteFunction(foo, Qualifier=2): %v", err)
	}

	got := listVersionNumbers(t, client, "foo")
	if got["2"] {
		t.Errorf("DeleteFunction(foo, Qualifier=2) left version 2 present; versions = %v", got)
	}

	if !got["$LATEST"] || !got["1"] {
		t.Errorf("DeleteFunction(foo, Qualifier=2) over-deleted; versions = %v, want $LATEST and 1 to remain", got)
	}
}

// TestSDKDeleteFunctionNoQualifierDeletesAll verifies an unqualified
// DeleteFunction still removes the whole function (all versions and aliases).
func TestSDKDeleteFunctionNoQualifierDeletesAll(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")
	publishV2(t, client, "foo")

	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo"),
	}); err != nil {
		t.Fatalf("DeleteFunction(foo): %v", err)
	}

	_, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo"),
	})
	if errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("GetFunctionConfiguration(foo) after full delete error = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKDeleteFunctionLatestQualifierErrors verifies deleting the $LATEST
// qualifier is rejected (real Lambda: "$LATEST version cannot be deleted without
// deleting the function.") and leaves every version intact.
func TestSDKDeleteFunctionLatestQualifierErrors(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	_, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo"),
		Qualifier:    aws.String("$LATEST"),
	})
	if errorCode(err) != "InvalidParameterValueException" {
		t.Fatalf("DeleteFunction(foo, Qualifier=$LATEST) error = %v, want InvalidParameterValueException", err)
	}

	if got := listVersionNumbers(t, client, "foo"); !got["$LATEST"] || !got["1"] {
		t.Errorf("DeleteFunction(foo, Qualifier=$LATEST) altered versions = %v, want $LATEST and 1 intact", got)
	}
}

// TestSDKDeleteFunctionMissingVersion verifies deleting a non-existent version
// qualifier is a ResourceNotFoundException and deletes nothing.
func TestSDKDeleteFunctionMissingVersion(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	_, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo:99"),
	})
	if errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("DeleteFunction(foo:99) error = %v, want ResourceNotFoundException", err)
	}

	if got := listVersionNumbers(t, client, "foo"); !got["$LATEST"] || !got["1"] {
		t.Errorf("DeleteFunction(foo:99) altered versions = %v, want $LATEST and 1 intact", got)
	}
}

// TestSDKDeleteFunctionVersionWithAliasErrors verifies a version an alias still
// references cannot be version-deleted (real Lambda: "You can't delete a version
// that an alias references." — ResourceConflictException) and the version
// survives.
func TestSDKDeleteFunctionVersionWithAliasErrors(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	if _, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("foo"),
		Name:            aws.String("prod"),
		FunctionVersion: aws.String("1"),
	}); err != nil {
		t.Fatalf("CreateAlias(foo, prod->1): %v", err)
	}

	_, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo:1"),
	})
	if errorCode(err) != "ResourceConflictException" {
		t.Fatalf("DeleteFunction(foo:1) with alias error = %v, want ResourceConflictException", err)
	}

	if got := listVersionNumbers(t, client, "foo"); !got["1"] {
		t.Errorf("DeleteFunction(foo:1) with alias removed version 1; versions = %v", got)
	}
}

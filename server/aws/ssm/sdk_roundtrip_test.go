package ssm_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSSMClient(t *testing.T) *awsssm.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SSM: cloud.SSM})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsssm.NewFromConfig(cfg, func(o *awsssm.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKPutGetParameter(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	put, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/db/host"),
		Value: aws.String("db.internal"),
		Type:  ssmtypes.ParameterTypeString,
	})
	if err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if put.Version != 1 {
		t.Fatalf("PutParameter version = %d, want 1", put.Version)
	}

	got, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/db/host")})
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if aws.ToString(got.Parameter.Value) != "db.internal" {
		t.Fatalf("value = %q, want db.internal", aws.ToString(got.Parameter.Value))
	}

	if got.Parameter.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Parameter.Version)
	}

	if got.Parameter.Type != ssmtypes.ParameterTypeString {
		t.Fatalf("type = %q, want String", got.Parameter.Type)
	}

	if aws.ToString(got.Parameter.ARN) == "" {
		t.Fatal("GetParameter returned empty ARN")
	}
}

func TestSDKPutOverwriteVersioning(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/key"),
		Value: aws.String("v1"),
		Type:  ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter v1: %v", err)
	}

	// Overwrite without the flag must fail.
	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/key"),
		Value: aws.String("v2"),
		Type:  ssmtypes.ParameterTypeString,
	})

	var exists *ssmtypes.ParameterAlreadyExists
	if !errors.As(err, &exists) {
		t.Fatalf("Put without Overwrite: got %v, want ParameterAlreadyExists", err)
	}

	put2, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:      aws.String("/app/key"),
		Value:     aws.String("v2"),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("PutParameter overwrite: %v", err)
	}

	if put2.Version != 2 {
		t.Fatalf("overwrite version = %d, want 2", put2.Version)
	}

	got, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/key")})
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if aws.ToString(got.Parameter.Value) != "v2" || got.Parameter.Version != 2 {
		t.Fatalf("got value %q version %d, want v2 version 2",
			aws.ToString(got.Parameter.Value), got.Parameter.Version)
	}

	// Fetch a specific historic version via the ":version" selector.
	old, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/key:1")})
	if err != nil {
		t.Fatalf("GetParameter(:1): %v", err)
	}

	if aws.ToString(old.Parameter.Value) != "v1" {
		t.Fatalf("v1 selector value = %q, want v1", aws.ToString(old.Parameter.Value))
	}
}

func TestSDKGetParametersByPath(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	seed := map[string]string{
		"/svc/a":      "1",
		"/svc/b":      "2",
		"/svc/deep/c": "3",
		"/other/x":    "9",
	}
	for name, val := range seed {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name:  aws.String(name),
			Value: aws.String(val),
			Type:  ssmtypes.ParameterTypeString,
		}); err != nil {
			t.Fatalf("PutParameter %s: %v", name, err)
		}
	}

	// Non-recursive: only direct children of /svc.
	shallow, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path: aws.String("/svc"),
	})
	if err != nil {
		t.Fatalf("GetParametersByPath(non-recursive): %v", err)
	}

	if names := paramNames(shallow.Parameters); !equalStrings(names, []string{"/svc/a", "/svc/b"}) {
		t.Fatalf("non-recursive names = %v, want [/svc/a /svc/b]", names)
	}

	// Recursive: whole subtree.
	deep, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path:      aws.String("/svc"),
		Recursive: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("GetParametersByPath(recursive): %v", err)
	}

	if names := paramNames(deep.Parameters); !equalStrings(names, []string{"/svc/a", "/svc/b", "/svc/deep/c"}) {
		t.Fatalf("recursive names = %v, want [/svc/a /svc/b /svc/deep/c]", names)
	}
}

func TestSDKGetParameters(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	for _, name := range []string{"/multi/one", "/multi/two"} {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name:  aws.String(name),
			Value: aws.String("val" + name),
			Type:  ssmtypes.ParameterTypeString,
		}); err != nil {
			t.Fatalf("PutParameter %s: %v", name, err)
		}
	}

	got, err := client.GetParameters(ctx, &awsssm.GetParametersInput{
		Names: []string{"/multi/one", "/multi/two", "/multi/missing"},
	})
	if err != nil {
		t.Fatalf("GetParameters: %v", err)
	}

	if len(got.Parameters) != 2 {
		t.Fatalf("got %d parameters, want 2", len(got.Parameters))
	}

	if len(got.InvalidParameters) != 1 || got.InvalidParameters[0] != "/multi/missing" {
		t.Fatalf("InvalidParameters = %v, want [/multi/missing]", got.InvalidParameters)
	}
}

func TestSDKDeleteParameter(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/del/me"),
		Value: aws.String("x"),
		Type:  ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if _, err := client.DeleteParameter(ctx, &awsssm.DeleteParameterInput{Name: aws.String("/del/me")}); err != nil {
		t.Fatalf("DeleteParameter: %v", err)
	}

	_, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/del/me")})

	var notFound *ssmtypes.ParameterNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("GetParameter after delete: got %v, want ParameterNotFound", err)
	}
}

func TestSDKGetParameterNotFound(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	_, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/does/not/exist")})

	var notFound *ssmtypes.ParameterNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("GetParameter(missing): got %v, want ParameterNotFound", err)
	}
}

func paramNames(ps []ssmtypes.Parameter) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, aws.ToString(p.Name))
	}

	sort.Strings(out)

	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

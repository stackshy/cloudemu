package codeartifact_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	ca "github.com/aws/aws-sdk-go-v2/service/codeartifact"
	catypes "github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// env is a running emulator with a real CodeArtifact SDK client pointed at it.
type env struct {
	c     *ca.Client
	cloud *awsprovider.Provider
	url   string
}

func newEnv(t *testing.T) *env {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{CodeArtifact: cloud.CodeArtifact})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return &env{
		c:     ca.NewFromConfig(cfg, func(o *ca.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		cloud: cloud,
		url:   ts.URL,
	}
}

// seed creates domain "dom" with the given repositories.
func (e *env) seed(t *testing.T, repos ...string) {
	t.Helper()

	ctx := context.Background()

	if _, err := e.c.CreateDomain(ctx, &ca.CreateDomainInput{Domain: aws.String("dom")}); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	for _, r := range repos {
		if _, err := e.c.CreateRepository(ctx, &ca.CreateRepositoryInput{Domain: aws.String("dom"), Repository: aws.String(r)}); err != nil {
			t.Fatalf("CreateRepository %s: %v", r, err)
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// publish publishes one asset of a generic package through the SDK.
func (e *env) publish(t *testing.T, repo, pkg, version, asset string, data []byte, unfinished bool) *ca.PublishPackageVersionOutput {
	t.Helper()

	in := &ca.PublishPackageVersionInput{
		Domain: aws.String("dom"), Repository: aws.String(repo), Format: catypes.PackageFormatGeneric,
		Namespace: aws.String("ns"), Package: aws.String(pkg), PackageVersion: aws.String(version),
		AssetName: aws.String(asset), AssetSHA256: aws.String(sha256Hex(data)), AssetContent: bytes.NewReader(data),
	}
	if unfinished {
		in.Unfinished = aws.Bool(true)
	}

	out, err := e.c.PublishPackageVersion(context.Background(), in)
	if err != nil {
		t.Fatalf("PublishPackageVersion: %v", err)
	}

	return out
}

// genericVersion is the SDK input naming one generic package version.
func pkgInput(repo, pkg string) (domain, repository *string, format catypes.PackageFormat, ns, name *string) {
	return aws.String("dom"), aws.String(repo), catypes.PackageFormatGeneric, aws.String("ns"), aws.String(pkg)
}

// requireAPICode asserts err is a smithy API error with the given code and HTTP
// status, and returns it.
func requireAPICode(t *testing.T, err error, code string, status int) smithy.APIError {
	t.Helper()

	if err == nil {
		t.Fatalf("want %s, got nil", code)
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want smithy.APIError %s, got %T: %v", code, err, err)
	}

	if apiErr.ErrorCode() != code {
		t.Fatalf("error code = %q, want %q (%v)", apiErr.ErrorCode(), code, err)
	}

	if status != 0 {
		var re interface{ HTTPStatusCode() int }
		if errors.As(err, &re) && re.HTTPStatusCode() != status {
			t.Fatalf("http status = %d, want %d", re.HTTPStatusCode(), status)
		}
	}

	return apiErr
}

func readAll(t *testing.T, r io.ReadCloser) []byte {
	t.Helper()

	defer r.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return b
}

package transfer_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awstransfer "github.com/aws/aws-sdk-go-v2/service/transfer"
	transfertypes "github.com/aws/aws-sdk-go-v2/service/transfer/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

var (
	serverIDRe = regexp.MustCompile(`^s-[0-9a-f]{17}$`)
	keyIDRe    = regexp.MustCompile(`^key-[0-9a-f]{17}$`)
)

func newTransferClient(t *testing.T) *awstransfer.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Transfer: cloud.Transfer})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awstransfer.NewFromConfig(cfg, func(o *awstransfer.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKServerRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newTransferClient(t)

	out, err := c.CreateServer(ctx, &awstransfer.CreateServerInput{
		Protocols:    []transfertypes.Protocol{transfertypes.ProtocolSftp},
		EndpointType: transfertypes.EndpointTypePublic,
		Tags: []transfertypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
		},
	})
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	id := aws.ToString(out.ServerId)
	if !serverIDRe.MatchString(id) {
		t.Fatalf("ServerId %q does not match s-<17hex>", id)
	}

	got, err := c.DescribeServer(ctx, &awstransfer.DescribeServerInput{ServerId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeServer: %v", err)
	}

	s := got.Server
	if s.State != transfertypes.StateOnline {
		t.Fatalf("State = %q, want ONLINE (synchronous)", s.State)
	}

	if s.Domain != transfertypes.DomainS3 {
		t.Fatalf("Domain = %q, want S3", s.Domain)
	}

	if s.EndpointType != transfertypes.EndpointTypePublic {
		t.Fatalf("EndpointType = %q, want PUBLIC", s.EndpointType)
	}

	if s.IdentityProviderType != transfertypes.IdentityProviderTypeServiceManaged {
		t.Fatalf("IdentityProviderType = %q, want SERVICE_MANAGED", s.IdentityProviderType)
	}

	if len(s.Protocols) != 1 || s.Protocols[0] != transfertypes.ProtocolSftp {
		t.Fatalf("Protocols = %v, want [SFTP]", s.Protocols)
	}

	if aws.ToString(s.SecurityPolicyName) == "" {
		t.Fatalf("SecurityPolicyName default not materialized")
	}

	if aws.ToString(s.HostKeyFingerprint) == "" {
		t.Fatalf("HostKeyFingerprint not set")
	}

	if aws.ToString(s.Arn) != "arn:aws:transfer:us-east-1:123456789012:server/"+id {
		t.Fatalf("Arn = %q", aws.ToString(s.Arn))
	}
}

func TestSDKUserAndSSHKeyRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newTransferClient(t)

	srv, err := c.CreateServer(ctx, &awstransfer.CreateServerInput{})
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	id := aws.ToString(srv.ServerId)

	_, err = c.CreateUser(ctx, &awstransfer.CreateUserInput{
		ServerId:      aws.String(id),
		UserName:      aws.String("alice"),
		Role:          aws.String("arn:aws:iam::123456789012:role/transfer"),
		HomeDirectory: aws.String("/bucket/alice"),
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	du, err := c.DescribeUser(ctx, &awstransfer.DescribeUserInput{
		ServerId: aws.String(id),
		UserName: aws.String("alice"),
	})
	if err != nil {
		t.Fatalf("DescribeUser: %v", err)
	}

	if du.User.HomeDirectoryType != transfertypes.HomeDirectoryTypePath {
		t.Fatalf("HomeDirectoryType = %q, want PATH", du.User.HomeDirectoryType)
	}

	if aws.ToString(du.User.Arn) != "arn:aws:transfer:us-east-1:123456789012:user/"+id+"/alice" {
		t.Fatalf("user Arn = %q", aws.ToString(du.User.Arn))
	}

	key, err := c.ImportSshPublicKey(ctx, &awstransfer.ImportSshPublicKeyInput{
		ServerId:         aws.String(id),
		UserName:         aws.String("alice"),
		SshPublicKeyBody: aws.String("ssh-rsa AAAAB3NzaC1yc2E test"),
	})
	if err != nil {
		t.Fatalf("ImportSshPublicKey: %v", err)
	}

	if !keyIDRe.MatchString(aws.ToString(key.SshPublicKeyId)) {
		t.Fatalf("SshPublicKeyId %q does not match key-<17hex>", aws.ToString(key.SshPublicKeyId))
	}

	du, err = c.DescribeUser(ctx, &awstransfer.DescribeUserInput{
		ServerId: aws.String(id),
		UserName: aws.String("alice"),
	})
	if err != nil {
		t.Fatalf("DescribeUser after import: %v", err)
	}

	if len(du.User.SshPublicKeys) != 1 {
		t.Fatalf("SshPublicKeyCount = %d, want 1", len(du.User.SshPublicKeys))
	}

	lu, err := c.ListUsers(ctx, &awstransfer.ListUsersInput{ServerId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(lu.Users) != 1 || aws.ToInt32(lu.Users[0].SshPublicKeyCount) != 1 {
		t.Fatalf("ListUsers summary unexpected: %+v", lu.Users)
	}
}

func TestSDKDescribeServerNotFound(t *testing.T) {
	ctx := context.Background()
	c := newTransferClient(t)

	_, err := c.DescribeServer(ctx, &awstransfer.DescribeServerInput{ServerId: aws.String("s-0000000000000000f")})
	if err == nil {
		t.Fatalf("expected ResourceNotFoundException")
	}

	var nf *transfertypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		var api smithy.APIError
		if errors.As(err, &api) {
			t.Fatalf("error type = %q, want ResourceNotFoundException", api.ErrorCode())
		}

		t.Fatalf("unexpected error: %v", err)
	}
}

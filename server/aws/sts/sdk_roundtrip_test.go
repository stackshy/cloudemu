package sts_test

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
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	testAccountID = "123456789012"
	testRegion    = "us-west-2"
)

func newServer(t *testing.T, d awsserver.Drivers) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(awsserver.New(d))
	t.Cleanup(ts.Close)

	return ts
}

func stsClient(t *testing.T, url string) *awssts.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssts.NewFromConfig(cfg, func(o *awssts.Options) {
		o.BaseEndpoint = aws.String(url)
	})
}

func TestSDKGetCallerIdentity(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).GetCallerIdentity(
		context.Background(), &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity: %v", err)
	}

	if aws.ToString(out.Account) != testAccountID {
		t.Errorf("Account = %q, want %q", aws.ToString(out.Account), testAccountID)
	}

	if arn := aws.ToString(out.Arn); !strings.Contains(arn, testAccountID) {
		t.Errorf("Arn = %q, want it to contain account id", arn)
	}

	if aws.ToString(out.UserId) == "" {
		t.Error("UserId is empty")
	}
}

func TestSDKAssumeRole(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	const roleArn = "arn:aws:iam::123456789012:role/MyTestRole"

	out, err := stsClient(t, ts.URL).AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String("my-session"),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	if out.AssumedRoleUser == nil {
		t.Fatal("AssumedRoleUser is nil")
	}

	gotArn := aws.ToString(out.AssumedRoleUser.Arn)
	wantArn := "arn:aws:sts::" + testAccountID + ":assumed-role/MyTestRole/my-session"

	if gotArn != wantArn {
		t.Errorf("AssumedRoleUser.Arn = %q, want %q", gotArn, wantArn)
	}

	if out.Credentials == nil {
		t.Fatal("Credentials is nil")
	}

	if aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Error("AccessKeyId is empty")
	}

	if aws.ToString(out.Credentials.SecretAccessKey) == "" {
		t.Error("SecretAccessKey is empty")
	}

	if aws.ToString(out.Credentials.SessionToken) == "" {
		t.Error("SessionToken is empty")
	}

	if out.Credentials.Expiration == nil {
		t.Error("Expiration is nil")
	}
}

func TestSDKGetSessionToken(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).GetSessionToken(
		context.Background(), &awssts.GetSessionTokenInput{})
	if err != nil {
		t.Fatalf("GetSessionToken: %v", err)
	}

	if out.Credentials == nil {
		t.Fatal("Credentials is nil")
	}

	if aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Error("AccessKeyId is empty")
	}

	if aws.ToString(out.Credentials.SessionToken) == "" {
		t.Error("SessionToken is empty")
	}
}

func TestSDKAssumeRoleWithWebIdentity(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).AssumeRoleWithWebIdentity(context.Background(),
		&awssts.AssumeRoleWithWebIdentityInput{
			RoleArn:          aws.String("arn:aws:iam::123456789012:role/IRSARole"),
			RoleSessionName:  aws.String("irsa-session"),
			WebIdentityToken: aws.String("eyJhbGciOi.fake.token"),
		})
	if err != nil {
		t.Fatalf("AssumeRoleWithWebIdentity: %v", err)
	}

	if out.Credentials == nil || aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Fatal("Credentials missing")
	}

	if out.AssumedRoleUser == nil {
		t.Fatal("AssumedRoleUser is nil")
	}

	wantArn := "arn:aws:sts::" + testAccountID + ":assumed-role/IRSARole/irsa-session"
	if got := aws.ToString(out.AssumedRoleUser.Arn); got != wantArn {
		t.Errorf("AssumedRoleUser.Arn = %q, want %q", got, wantArn)
	}

	if aws.ToString(out.SubjectFromWebIdentityToken) == "" {
		t.Error("SubjectFromWebIdentityToken is empty")
	}
}

func TestSDKAssumeRoleWithSAML(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).AssumeRoleWithSAML(context.Background(),
		&awssts.AssumeRoleWithSAMLInput{
			RoleArn:       aws.String("arn:aws:iam::123456789012:role/SAMLRole"),
			PrincipalArn:  aws.String("arn:aws:iam::123456789012:saml-provider/Corp"),
			SAMLAssertion: aws.String("base64assertion"),
		})
	if err != nil {
		t.Fatalf("AssumeRoleWithSAML: %v", err)
	}

	if out.Credentials == nil || aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Fatal("Credentials missing")
	}

	if out.AssumedRoleUser == nil {
		t.Fatal("AssumedRoleUser is nil")
	}

	if aws.ToString(out.Subject) == "" {
		t.Error("Subject is empty")
	}
}

func TestSDKGetFederationToken(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).GetFederationToken(context.Background(),
		&awssts.GetFederationTokenInput{Name: aws.String("app-user")})
	if err != nil {
		t.Fatalf("GetFederationToken: %v", err)
	}

	if out.Credentials == nil || aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Fatal("Credentials missing")
	}

	if out.FederatedUser == nil {
		t.Fatal("FederatedUser is nil")
	}

	wantArn := "arn:aws:sts::" + testAccountID + ":federated-user/app-user"
	if got := aws.ToString(out.FederatedUser.Arn); got != wantArn {
		t.Errorf("FederatedUser.Arn = %q, want %q", got, wantArn)
	}
}

func TestSDKGetAccessKeyInfo(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).GetAccessKeyInfo(context.Background(),
		&awssts.GetAccessKeyInfoInput{AccessKeyId: aws.String("AKIAEXAMPLE")})
	if err != nil {
		t.Fatalf("GetAccessKeyInfo: %v", err)
	}

	if aws.ToString(out.Account) != testAccountID {
		t.Errorf("Account = %q, want %q", aws.ToString(out.Account), testAccountID)
	}
}

func TestSDKDecodeAuthorizationMessage(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).DecodeAuthorizationMessage(context.Background(),
		&awssts.DecodeAuthorizationMessageInput{EncodedMessage: aws.String("encoded-blob")})
	if err != nil {
		t.Fatalf("DecodeAuthorizationMessage: %v", err)
	}

	if aws.ToString(out.DecodedMessage) == "" {
		t.Error("DecodedMessage is empty")
	}
}

// TestSDKAssumeRoleHonorsDuration proves DurationSeconds shortens the credential
// expiration rather than always returning ~now+1h.
func TestSDKAssumeRoleHonorsDuration(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::123456789012:role/MyTestRole"),
		RoleSessionName: aws.String("short-session"),
		DurationSeconds: aws.Int32(900),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	if out.Credentials == nil || out.Credentials.Expiration == nil {
		t.Fatal("Expiration is nil")
	}

	ttl := time.Until(*out.Credentials.Expiration)
	if ttl > 30*time.Minute {
		t.Errorf("Expiration TTL = %v, want it honored near 900s (well under the 1h default)", ttl)
	}
}

// TestSTSDoesNotShadowEC2 wires STS alongside EC2 and proves an EC2 action
// (RunInstances) still reaches the EC2 handler: STS's Matches only claims its
// own action set, so a query-protocol body bound for EC2 falls through.
func TestSTSDoesNotShadowEC2(t *testing.T) {
	cloud := cloudemu.NewAWS()

	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		EC2:       cloud.EC2,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	ec2cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ec2c := awsec2.NewFromConfig(ec2cfg, func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	runOut, err := ec2c.RunInstances(context.Background(), &awsec2.RunInstancesInput{
		ImageId:      aws.String("ami-12345678"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances (should reach EC2, not STS): %v", err)
	}

	if len(runOut.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(runOut.Instances))
	}

	// And STS still works on the same server.
	idOut, err := stsClient(t, ts.URL).GetCallerIdentity(
		context.Background(), &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity alongside EC2: %v", err)
	}

	if aws.ToString(idOut.Account) != testAccountID {
		t.Errorf("Account = %q, want %q", aws.ToString(idOut.Account), testAccountID)
	}
}

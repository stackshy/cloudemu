package sts_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// stsClientWithCreds is like stsClient but signs requests with the given
// access key/secret instead of the fixed "test"/"test" pair, so a test can
// prove GetCallerIdentity reflects the presented credential.
func stsClientWithCreds(t *testing.T, url, akid, secret string) *awssts.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(akid, secret, ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssts.NewFromConfig(cfg, func(o *awssts.Options) {
		o.BaseEndpoint = aws.String(url)
	})
}

// TestGetCallerIdentityReflectsIAMUser proves GetCallerIdentity reports the
// IAM user owning the presented access key — not a hardcoded identity —  by
// creating a real user + access key through the IAM driver and calling
// GetCallerIdentity with the real aws-sdk-go-v2 STS client signed with that
// key.
func TestGetCallerIdentityReflectsIAMUser(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "alice"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "alice"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		IAM:       cloud.IAM,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClientWithCreds(t, ts.URL, ak.AccessKeyID, ak.SecretAccessKey).
		GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity: %v", err)
	}

	wantArn := "arn:aws:iam::" + testAccountID + ":user/alice"
	if got := aws.ToString(out.Arn); got != wantArn {
		t.Errorf("Arn = %q, want %q", got, wantArn)
	}

	if aws.ToString(out.UserId) == "AIDACLOUDEMU0000000000" {
		t.Error("UserId is the hardcoded placeholder, not derived from the real IAM user")
	}

	if aws.ToString(out.Account) != testAccountID {
		t.Errorf("Account = %q, want %q", aws.ToString(out.Account), testAccountID)
	}
}

// TestGetCallerIdentityReflectsAssumedRole proves that after AssumeRole, using
// the returned temporary credentials to call GetCallerIdentity reports the
// assumed-role ARN, not the caller's own or a hardcoded identity.
func TestGetCallerIdentityReflectsAssumedRole(t *testing.T) {
	ctx := context.Background()

	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	const roleArn = "arn:aws:iam::123456789012:role/DeployRole"

	assumed, err := stsClient(t, ts.URL).AssumeRole(ctx, &awssts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String("e2e-session"),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	creds := assumed.Credentials

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			aws.ToString(creds.AccessKeyId), aws.ToString(creds.SecretAccessKey), aws.ToString(creds.SessionToken),
		)),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	assumedClient := awssts.NewFromConfig(cfg, func(o *awssts.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	ident, err := assumedClient.GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity (assumed role): %v", err)
	}

	wantArn := "arn:aws:sts::" + testAccountID + ":assumed-role/DeployRole/e2e-session"
	if got := aws.ToString(ident.Arn); got != wantArn {
		t.Errorf("Arn = %q, want %q", got, wantArn)
	}
}

// TestGetCallerIdentityDistinctForDistinctKeys proves two different presented
// access keys resolve to two different identities — GetCallerIdentity is not
// collapsing every caller onto one constant.
func TestGetCallerIdentityDistinctForDistinctKeys(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	for _, name := range []string{"bob", "carol"} {
		if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: name}); err != nil {
			t.Fatalf("CreateUser(%s): %v", name, err)
		}
	}

	bobKey, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "bob"})
	if err != nil {
		t.Fatalf("CreateAccessKey(bob): %v", err)
	}

	carolKey, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "carol"})
	if err != nil {
		t.Fatalf("CreateAccessKey(carol): %v", err)
	}

	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		IAM:       cloud.IAM,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	bobIdent, err := stsClientWithCreds(t, ts.URL, bobKey.AccessKeyID, bobKey.SecretAccessKey).
		GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity(bob): %v", err)
	}

	carolIdent, err := stsClientWithCreds(t, ts.URL, carolKey.AccessKeyID, carolKey.SecretAccessKey).
		GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity(carol): %v", err)
	}

	if aws.ToString(bobIdent.Arn) == aws.ToString(carolIdent.Arn) {
		t.Errorf("bob and carol resolved to the same Arn %q; identity is a constant", aws.ToString(bobIdent.Arn))
	}

	if aws.ToString(bobIdent.UserId) == aws.ToString(carolIdent.UserId) {
		t.Errorf("bob and carol resolved to the same UserId %q; identity is a constant", aws.ToString(bobIdent.UserId))
	}
}

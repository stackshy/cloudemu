package cognito_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	ciptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newCognitoClient(t *testing.T) *cip.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Cognito: cloud.Cognito})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cip.NewFromConfig(cfg, func(o *cip.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createPool(t *testing.T, c *cip.Client, name string) string {
	t.Helper()

	out, err := c.CreateUserPool(context.Background(), &cip.CreateUserPoolInput{
		PoolName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("CreateUserPool: %v", err)
	}

	return aws.ToString(out.UserPool.Id)
}

func TestSDKUserPoolRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)

	created, err := c.CreateUserPool(ctx, &cip.CreateUserPoolInput{
		PoolName:               aws.String("app-users"),
		AutoVerifiedAttributes: []ciptypes.VerifiedAttributeType{ciptypes.VerifiedAttributeTypeEmail},
		Policies: &ciptypes.UserPoolPolicyType{
			PasswordPolicy: &ciptypes.PasswordPolicyType{
				MinimumLength:    aws.Int32(12),
				RequireSymbols:   false,
				RequireNumbers:   true,
				RequireLowercase: true,
				RequireUppercase: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateUserPool: %v", err)
	}

	id := aws.ToString(created.UserPool.Id)

	// Id format: "<region>_<9 alphanumeric>".
	if !strings.HasPrefix(id, "us-east-1_") || len(strings.TrimPrefix(id, "us-east-1_")) != 9 {
		t.Fatalf("unexpected pool id format: %q", id)
	}

	wantARN := "arn:aws:cognito-idp:us-east-1:123456789012:userpool/" + id
	if got := aws.ToString(created.UserPool.Arn); got != wantARN {
		t.Fatalf("ARN = %q, want %q", got, wantARN)
	}

	if created.UserPool.CreationDate == nil {
		t.Fatal("CreationDate not set (epoch float expected)")
	}

	got, err := c.DescribeUserPool(ctx, &cip.DescribeUserPoolInput{UserPoolId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeUserPool: %v", err)
	}

	pool := got.UserPool

	assertPoolDefaults(t, pool)

	// Password policy round-trips, including the explicit false for symbols.
	pp := pool.Policies.PasswordPolicy
	if aws.ToInt32(pp.MinimumLength) != 12 || pp.RequireSymbols {
		t.Fatalf("password policy did not round-trip: %+v", pp)
	}

	if pp.TemporaryPasswordValidityDays != 7 {
		t.Fatalf("TemporaryPasswordValidityDays = %d, want 7", pp.TemporaryPasswordValidityDays)
	}
}

func assertPoolDefaults(t *testing.T, pool *ciptypes.UserPoolType) {
	t.Helper()

	if pool.MfaConfiguration != ciptypes.UserPoolMfaTypeOff {
		t.Fatalf("MfaConfiguration = %q, want OFF", pool.MfaConfiguration)
	}

	if pool.DeletionProtection != ciptypes.DeletionProtectionTypeInactive {
		t.Fatalf("DeletionProtection = %q, want INACTIVE", pool.DeletionProtection)
	}

	if len(pool.SchemaAttributes) != 20 {
		t.Fatalf("SchemaAttributes count = %d, want 20", len(pool.SchemaAttributes))
	}

	// The seeded schema includes the immutable required "sub" attribute.
	var foundSub bool

	for _, a := range pool.SchemaAttributes {
		if aws.ToString(a.Name) == "sub" {
			foundSub = true

			if aws.ToBool(a.Mutable) || !aws.ToBool(a.Required) {
				t.Fatalf("sub attribute wrong: mutable=%v required=%v", aws.ToBool(a.Mutable), aws.ToBool(a.Required))
			}
		}
	}

	if !foundSub {
		t.Fatal("default schema missing sub attribute")
	}
}

func TestSDKUserPoolClientSecretGeneration(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	id := createPool(t, c, "clients-pool")

	// Without GenerateSecret, no secret is returned.
	noSecret, err := c.CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId: aws.String(id),
		ClientName: aws.String("public-client"),
	})
	if err != nil {
		t.Fatalf("CreateUserPoolClient (no secret): %v", err)
	}

	if s := aws.ToString(noSecret.UserPoolClient.ClientSecret); s != "" {
		t.Fatalf("client secret returned without GenerateSecret: %q", s)
	}

	client := noSecret.UserPoolClient
	if len(aws.ToString(client.ClientId)) != 26 {
		t.Fatalf("ClientId length = %d, want 26", len(aws.ToString(client.ClientId)))
	}

	if client.RefreshTokenValidity != 30 {
		t.Fatalf("RefreshTokenValidity = %d, want 30", client.RefreshTokenValidity)
	}

	if aws.ToInt32(client.AuthSessionValidity) != 3 {
		t.Fatalf("AuthSessionValidity = %d, want 3", aws.ToInt32(client.AuthSessionValidity))
	}

	if !aws.ToBool(client.EnableTokenRevocation) {
		t.Fatal("EnableTokenRevocation = false, want true")
	}

	if client.PreventUserExistenceErrors != ciptypes.PreventUserExistenceErrorTypesLegacy {
		t.Fatalf("PreventUserExistenceErrors = %q, want LEGACY", client.PreventUserExistenceErrors)
	}

	assertDefaultAuthFlows(t, client)

	// An unconfigured client reports empty token-validity units (real Cognito
	// returns them empty so the Terraform provider collapses the block to zero
	// and does not drift).
	if u := client.TokenValidityUnits; u != nil &&
		(u.AccessToken != "" || u.IdToken != "" || u.RefreshToken != "") {
		t.Fatalf("TokenValidityUnits should be empty when unset: %+v", u)
	}

	// With GenerateSecret, a 51-character secret is returned.
	withSecret, err := c.CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId:     aws.String(id),
		ClientName:     aws.String("confidential-client"),
		GenerateSecret: true,
	})
	if err != nil {
		t.Fatalf("CreateUserPoolClient (secret): %v", err)
	}

	if s := aws.ToString(withSecret.UserPoolClient.ClientSecret); len(s) != 51 {
		t.Fatalf("client secret length = %d, want 51", len(s))
	}
}

func assertDefaultAuthFlows(t *testing.T, client *ciptypes.UserPoolClientType) {
	t.Helper()

	wantFlows := map[ciptypes.ExplicitAuthFlowsType]bool{
		ciptypes.ExplicitAuthFlowsTypeAllowRefreshTokenAuth: true,
		ciptypes.ExplicitAuthFlowsTypeAllowUserSrpAuth:      true,
		ciptypes.ExplicitAuthFlowsTypeAllowCustomAuth:       true,
	}
	if len(client.ExplicitAuthFlows) != len(wantFlows) {
		t.Fatalf("ExplicitAuthFlows = %v, want the 3-flow default", client.ExplicitAuthFlows)
	}

	for _, f := range client.ExplicitAuthFlows {
		if !wantFlows[f] {
			t.Fatalf("unexpected auth flow %q", f)
		}
	}
}

func TestSDKUserPoolClientExplicitTokenUnits(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	id := createPool(t, c, "units-pool")

	out, err := c.CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId:          aws.String(id),
		ClientName:          aws.String("units-client"),
		AccessTokenValidity: aws.Int32(2),
		TokenValidityUnits: &ciptypes.TokenValidityUnitsType{
			AccessToken: ciptypes.TimeUnitsTypeMinutes,
		},
	})
	if err != nil {
		t.Fatalf("CreateUserPoolClient: %v", err)
	}

	client := out.UserPoolClient
	if aws.ToInt32(client.AccessTokenValidity) != 2 {
		t.Fatalf("AccessTokenValidity = %d, want 2", aws.ToInt32(client.AccessTokenValidity))
	}

	if client.TokenValidityUnits == nil || client.TokenValidityUnits.AccessToken != ciptypes.TimeUnitsTypeMinutes {
		t.Fatalf("explicit TokenValidityUnits did not round-trip: %+v", client.TokenValidityUnits)
	}
}

func TestSDKGetUserPoolMfaConfig(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	id := createPool(t, c, "mfa-pool")

	got, err := c.GetUserPoolMfaConfig(ctx, &cip.GetUserPoolMfaConfigInput{UserPoolId: aws.String(id)})
	if err != nil {
		t.Fatalf("GetUserPoolMfaConfig: %v", err)
	}

	if got.MfaConfiguration != ciptypes.UserPoolMfaTypeOff {
		t.Fatalf("MfaConfiguration = %q, want OFF", got.MfaConfiguration)
	}

	_, err = c.SetUserPoolMfaConfig(ctx, &cip.SetUserPoolMfaConfigInput{
		UserPoolId:       aws.String(id),
		MfaConfiguration: ciptypes.UserPoolMfaTypeOptional,
		SoftwareTokenMfaConfiguration: &ciptypes.SoftwareTokenMfaConfigType{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("SetUserPoolMfaConfig: %v", err)
	}

	after, err := c.GetUserPoolMfaConfig(ctx, &cip.GetUserPoolMfaConfigInput{UserPoolId: aws.String(id)})
	if err != nil {
		t.Fatalf("GetUserPoolMfaConfig (after set): %v", err)
	}

	if after.MfaConfiguration != ciptypes.UserPoolMfaTypeOptional {
		t.Fatalf("MfaConfiguration after set = %q, want OPTIONAL", after.MfaConfiguration)
	}

	if after.SoftwareTokenMfaConfiguration == nil || !after.SoftwareTokenMfaConfiguration.Enabled {
		t.Fatalf("SoftwareTokenMfaConfiguration not applied: %+v", after.SoftwareTokenMfaConfiguration)
	}
}

func TestSDKUserPoolDomainUnknownReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	id := createPool(t, c, "domain-pool")

	created, err := c.CreateUserPoolDomain(ctx, &cip.CreateUserPoolDomainInput{
		Domain:     aws.String("my-login-domain"),
		UserPoolId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("CreateUserPoolDomain: %v", err)
	}

	if aws.ToString(created.CloudFrontDomain) == "" {
		t.Fatal("CloudFrontDomain empty on create")
	}

	desc, err := c.DescribeUserPoolDomain(ctx, &cip.DescribeUserPoolDomainInput{
		Domain: aws.String("my-login-domain"),
	})
	if err != nil {
		t.Fatalf("DescribeUserPoolDomain: %v", err)
	}

	if desc.DomainDescription.Status != ciptypes.DomainStatusTypeActive {
		t.Fatalf("domain status = %q, want ACTIVE", desc.DomainDescription.Status)
	}

	// An unknown domain returns an empty DomainDescription, not an error.
	unknown, err := c.DescribeUserPoolDomain(ctx, &cip.DescribeUserPoolDomainInput{
		Domain: aws.String("does-not-exist"),
	})
	if err != nil {
		t.Fatalf("DescribeUserPoolDomain(unknown): %v", err)
	}

	if unknown.DomainDescription != nil && aws.ToString(unknown.DomainDescription.Domain) != "" {
		t.Fatalf("unknown domain returned a populated description: %+v", unknown.DomainDescription)
	}
}

func TestSDKUpdateUserPoolAndErrors(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	id := createPool(t, c, "update-pool")

	_, err := c.UpdateUserPool(ctx, &cip.UpdateUserPoolInput{
		UserPoolId:         aws.String(id),
		MfaConfiguration:   ciptypes.UserPoolMfaTypeOptional,
		DeletionProtection: ciptypes.DeletionProtectionTypeActive,
	})
	if err != nil {
		t.Fatalf("UpdateUserPool: %v", err)
	}

	got, err := c.DescribeUserPool(ctx, &cip.DescribeUserPoolInput{UserPoolId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeUserPool: %v", err)
	}

	if got.UserPool.MfaConfiguration != ciptypes.UserPoolMfaTypeOptional {
		t.Fatalf("MfaConfiguration not updated: %q", got.UserPool.MfaConfiguration)
	}

	if got.UserPool.DeletionProtection != ciptypes.DeletionProtectionTypeActive {
		t.Fatalf("DeletionProtection not updated: %q", got.UserPool.DeletionProtection)
	}

	// A missing pool surfaces the typed ResourceNotFoundException.
	_, err = c.DescribeUserPool(ctx, &cip.DescribeUserPoolInput{UserPoolId: aws.String("us-east-1_missing00")})

	var rnf *ciptypes.ResourceNotFoundException
	if !errors.As(err, &rnf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("expected ResourceNotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

package cognito_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsct "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	ciptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func requireErrorCode(t *testing.T, err error, code, msg string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected %s, got %v", code, err)
	}

	if apiErr.ErrorCode() != code {
		t.Fatalf("error code = %q, want %q (%s)", apiErr.ErrorCode(), code, apiErr.ErrorMessage())
	}

	if msg != "" && apiErr.ErrorMessage() != msg {
		t.Fatalf("message = %q, want %q", apiErr.ErrorMessage(), msg)
	}
}

func attr(attrs []ciptypes.AttributeType, name string) string {
	for _, a := range attrs {
		if aws.ToString(a.Name) == name {
			return aws.ToString(a.Value)
		}
	}

	return ""
}

func TestSDKAdminUserLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	pool := createPool(t, c, "users")

	created, err := c.AdminCreateUser(ctx, &cip.AdminCreateUserInput{
		UserPoolId:        aws.String(pool),
		Username:          aws.String("alice"),
		TemporaryPassword: aws.String("Temp0rary!pw"),
		MessageAction:     ciptypes.MessageActionTypeSuppress,
		UserAttributes: []ciptypes.AttributeType{
			{Name: aws.String("email"), Value: aws.String("alice@example.com")},
		},
	})
	if err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}

	u := created.User
	if aws.ToString(u.Username) != "alice" || !u.Enabled || u.UserStatus != ciptypes.UserStatusTypeForceChangePassword {
		t.Fatalf("created user = %+v", u)
	}

	if len(attr(u.Attributes, "sub")) != 36 || u.UserCreateDate == nil {
		t.Fatalf("created user missing sub or dates: %+v", u)
	}

	_, err = c.AdminCreateUser(ctx, &cip.AdminCreateUserInput{UserPoolId: aws.String(pool), Username: aws.String("alice")})
	requireErrorCode(t, err, "UsernameExistsException", "User account already exists")

	_, err = c.AdminSetUserPassword(ctx, &cip.AdminSetUserPasswordInput{
		UserPoolId: aws.String(pool), Username: aws.String("alice"), Password: aws.String("short"), Permanent: true,
	})
	requireErrorCode(t, err, "InvalidPasswordException", "Password did not conform with policy: Password not long enough")

	_, err = c.AdminResetUserPassword(ctx, &cip.AdminResetUserPasswordInput{UserPoolId: aws.String(pool), Username: aws.String("alice")})
	requireErrorCode(t, err, "NotAuthorizedException", "User password cannot be reset in the current state.")

	if _, err = c.AdminSetUserPassword(ctx, &cip.AdminSetUserPasswordInput{
		UserPoolId: aws.String(pool), Username: aws.String("alice"), Password: aws.String("Str0ng!Password"), Permanent: true,
	}); err != nil {
		t.Fatalf("AdminSetUserPassword: %v", err)
	}

	if _, err = c.AdminUpdateUserAttributes(ctx, &cip.AdminUpdateUserAttributesInput{
		UserPoolId: aws.String(pool), Username: aws.String("alice"),
		UserAttributes: []ciptypes.AttributeType{{Name: aws.String("name"), Value: aws.String("Alice")}},
	}); err != nil {
		t.Fatalf("AdminUpdateUserAttributes: %v", err)
	}

	if _, err = c.AdminDisableUser(ctx, &cip.AdminDisableUserInput{UserPoolId: aws.String(pool), Username: aws.String("alice")}); err != nil {
		t.Fatalf("AdminDisableUser: %v", err)
	}

	got, err := c.AdminGetUser(ctx, &cip.AdminGetUserInput{UserPoolId: aws.String(pool), Username: aws.String("alice")})
	if err != nil {
		t.Fatalf("AdminGetUser: %v", err)
	}

	if got.Enabled || got.UserStatus != ciptypes.UserStatusTypeConfirmed || attr(got.UserAttributes, "name") != "Alice" {
		t.Fatalf("AdminGetUser = enabled %v status %s attrs %+v", got.Enabled, got.UserStatus, got.UserAttributes)
	}

	if _, err = c.AdminDeleteUserAttributes(ctx, &cip.AdminDeleteUserAttributesInput{
		UserPoolId: aws.String(pool), Username: aws.String("alice"), UserAttributeNames: []string{"name"},
	}); err != nil {
		t.Fatalf("AdminDeleteUserAttributes: %v", err)
	}

	if _, err = c.AdminEnableUser(ctx, &cip.AdminEnableUserInput{UserPoolId: aws.String(pool), Username: aws.String("alice")}); err != nil {
		t.Fatalf("AdminEnableUser: %v", err)
	}

	if _, err = c.AdminResetUserPassword(ctx, &cip.AdminResetUserPasswordInput{
		UserPoolId: aws.String(pool), Username: aws.String("alice"),
	}); err != nil {
		t.Fatalf("AdminResetUserPassword: %v", err)
	}

	listed, err := c.ListUsers(ctx, &cip.ListUsersInput{UserPoolId: aws.String(pool), Filter: aws.String(`email = "alice@example.com"`)})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(listed.Users) != 1 || listed.Users[0].UserStatus != ciptypes.UserStatusTypeResetRequired ||
		attr(listed.Users[0].Attributes, "name") != "" {
		t.Fatalf("ListUsers = %+v", listed.Users)
	}

	desc, err := c.DescribeUserPool(ctx, &cip.DescribeUserPoolInput{UserPoolId: aws.String(pool)})
	if err != nil {
		t.Fatalf("DescribeUserPool: %v", err)
	}

	if desc.UserPool.EstimatedNumberOfUsers != 1 {
		t.Fatalf("EstimatedNumberOfUsers = %d, want 1", desc.UserPool.EstimatedNumberOfUsers)
	}

	if _, err = c.AdminDeleteUser(ctx, &cip.AdminDeleteUserInput{UserPoolId: aws.String(pool), Username: aws.String("alice")}); err != nil {
		t.Fatalf("AdminDeleteUser: %v", err)
	}

	_, err = c.AdminGetUser(ctx, &cip.AdminGetUserInput{UserPoolId: aws.String(pool), Username: aws.String("alice")})

	var unf *ciptypes.UserNotFoundException
	if !errors.As(err, &unf) {
		t.Fatalf("expected typed UserNotFoundException, got %v", err)
	}
}

func TestSDKAliasExistsAndForceAliasCreation(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)

	created, err := c.CreateUserPool(ctx, &cip.CreateUserPoolInput{
		PoolName:        aws.String("alias"),
		AliasAttributes: []ciptypes.AliasAttributeType{ciptypes.AliasAttributeTypeEmail},
	})
	if err != nil {
		t.Fatalf("CreateUserPool: %v", err)
	}

	pool := created.UserPool.Id
	verified := []ciptypes.AttributeType{
		{Name: aws.String("email"), Value: aws.String("dup@example.com")},
		{Name: aws.String("email_verified"), Value: aws.String("true")},
	}

	newUser := func(name string, force bool) error {
		_, err := c.AdminCreateUser(ctx, &cip.AdminCreateUserInput{
			UserPoolId: pool, Username: aws.String(name), UserAttributes: verified,
			MessageAction: ciptypes.MessageActionTypeSuppress, ForceAliasCreation: force,
		})

		return err
	}

	if err := newUser("first", false); err != nil {
		t.Fatalf("AdminCreateUser first: %v", err)
	}

	// AdminCreateUser does not model AliasExistsException, so the SDK surfaces
	// it as a generic API error carrying the code.
	requireErrorCode(t, newUser("second", false), "AliasExistsException", "An account with the given email already exists.")

	if err := newUser("second", true); err != nil {
		t.Fatalf("AdminCreateUser ForceAliasCreation: %v", err)
	}

	first, err := c.AdminGetUser(ctx, &cip.AdminGetUserInput{UserPoolId: pool, Username: aws.String("first")})
	if err != nil {
		t.Fatalf("AdminGetUser: %v", err)
	}

	if attr(first.UserAttributes, "email_verified") != "false" {
		t.Fatalf("old alias owner still verified: %+v", first.UserAttributes)
	}
}

func TestSDKListUsersPaginatorAndFilterErrors(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	pool := createPool(t, c, "paged")

	for i := range 62 {
		if _, err := c.AdminCreateUser(ctx, &cip.AdminCreateUserInput{
			UserPoolId: aws.String(pool), Username: aws.String(fmt.Sprintf("u%02d", i)),
			MessageAction: ciptypes.MessageActionTypeSuppress,
		}); err != nil {
			t.Fatalf("AdminCreateUser: %v", err)
		}
	}

	pages, total := 0, 0

	p := cip.NewListUsersPaginator(c, &cip.ListUsersInput{UserPoolId: aws.String(pool)})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListUsers page: %v", err)
		}

		pages++
		total += len(out.Users)
	}

	if pages != 2 || total != 62 {
		t.Fatalf("paginator saw %d pages, %d users; want 2 pages, 62 users", pages, total)
	}

	_, err := c.ListUsers(ctx, &cip.ListUsersInput{UserPoolId: aws.String(pool), Filter: aws.String(`custom:x = "1"`)})
	requireErrorCode(t, err, "InvalidParameterException", "Invalid search attribute: custom:x")
}

func TestSDKListUserPoolsPastSixty(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)

	for i := range 61 {
		createPool(t, c, fmt.Sprintf("pool-%02d", i))
	}

	total := 0

	p := cip.NewListUserPoolsPaginator(c, &cip.ListUserPoolsInput{MaxResults: aws.Int32(60)})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListUserPools page: %v", err)
		}

		total += len(out.UserPools)
	}

	if total != 61 {
		t.Fatalf("paginator saw %d pools, want 61", total)
	}

	_, err := c.ListUserPools(ctx, &cip.ListUserPoolsInput{MaxResults: aws.Int32(61)})
	requireErrorCode(t, err, "InvalidParameterException", "")
}

func TestSDKDeleteUserPoolBlockedByDomain(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	pool := createPool(t, c, "with-domain")

	if _, err := c.CreateUserPoolDomain(ctx, &cip.CreateUserPoolDomainInput{
		UserPoolId: aws.String(pool), Domain: aws.String("my-auth"),
	}); err != nil {
		t.Fatalf("CreateUserPoolDomain: %v", err)
	}

	_, err := c.DeleteUserPool(ctx, &cip.DeleteUserPoolInput{UserPoolId: aws.String(pool)})

	var ipe *ciptypes.InvalidParameterException
	if !errors.As(err, &ipe) || aws.ToString(ipe.Message) !=
		"User pool cannot be deleted. It has a domain configured that should be deleted first." {
		t.Fatalf("expected InvalidParameterException for attached domain, got %v", err)
	}

	if _, err = c.DeleteUserPoolDomain(ctx, &cip.DeleteUserPoolDomainInput{
		UserPoolId: aws.String(pool), Domain: aws.String("my-auth"),
	}); err != nil {
		t.Fatalf("DeleteUserPoolDomain: %v", err)
	}

	if _, err = c.DeleteUserPool(ctx, &cip.DeleteUserPoolInput{UserPoolId: aws.String(pool)}); err != nil {
		t.Fatalf("DeleteUserPool after domain removed: %v", err)
	}
}

func TestSDKAddCustomAttributes(t *testing.T) {
	ctx := context.Background()
	c := newCognitoClient(t)
	pool := createPool(t, c, "custom")

	in := &cip.AddCustomAttributesInput{
		UserPoolId: aws.String(pool),
		CustomAttributes: []ciptypes.SchemaAttributeType{{
			Name: aws.String("tier"), AttributeDataType: ciptypes.AttributeDataTypeString, Mutable: aws.Bool(true),
		}},
	}
	if _, err := c.AddCustomAttributes(ctx, in); err != nil {
		t.Fatalf("AddCustomAttributes: %v", err)
	}

	_, err := c.AddCustomAttributes(ctx, in)
	requireErrorCode(t, err, "InvalidParameterException", "Existing attribute already has name custom:tier.")

	desc, err := c.DescribeUserPool(ctx, &cip.DescribeUserPoolInput{UserPoolId: aws.String(pool)})
	if err != nil {
		t.Fatalf("DescribeUserPool: %v", err)
	}

	found := false

	for _, a := range desc.UserPool.SchemaAttributes {
		if aws.ToString(a.Name) == "custom:tier" {
			found = true
		}
	}

	if !found {
		t.Fatal("custom:tier missing from SchemaAttributes")
	}
}

// TestTagOpsReturnEmptyBody pins the exact TagResource / UntagResource output:
// real Cognito returns an empty JSON object.
func TestTagOpsReturnEmptyBody(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Cognito: cloud.Cognito}))
	t.Cleanup(ts.Close)

	arn := "arn:aws:cognito-idp:us-east-1:123456789012:userpool/us-east-1_abcdefghi"

	for op, body := range map[string]string{
		"TagResource":   `{"ResourceArn":"` + arn + `","Tags":{"env":"dev"}}`,
		"UntagResource": `{"ResourceArn":"` + arn + `","TagKeys":["env"]}`,
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+op)
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}

		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != "{}" {
			t.Fatalf("%s = %d %q, want 200 {}", op, resp.StatusCode, raw)
		}
	}
}

// TestCloudTrailRecordsCognitoCalls pins that Cognito control-plane and admin
// user calls show up in CloudTrail LookupEvents.
func TestCloudTrailRecordsCognitoCalls(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)
	c := cip.NewFromConfig(cfg)
	pool := createPool(t, c, "audited")

	if _, err = c.AdminCreateUser(ctx, &cip.AdminCreateUserInput{
		UserPoolId: aws.String(pool), Username: aws.String("audit-user"), MessageAction: ciptypes.MessageActionTypeSuppress,
	}); err != nil {
		t.Fatalf("AdminCreateUser: %v", err)
	}

	out, err := awsct.NewFromConfig(cfg).LookupEvents(ctx, &awsct.LookupEventsInput{})
	if err != nil {
		t.Fatalf("LookupEvents: %v", err)
	}

	names := map[string]string{}
	for _, e := range out.Events {
		names[aws.ToString(e.EventName)] = aws.ToString(e.EventSource)
	}

	for _, want := range []string{"CreateUserPool", "AdminCreateUser"} {
		if src, ok := names[want]; !ok || src != "cognito-idp.amazonaws.com" {
			t.Fatalf("%s not recorded from cognito-idp.amazonaws.com; events = %v", want, names)
		}
	}
}

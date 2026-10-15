package sts_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
)

// TestEnforcedTrustRoleARNIsLimitedByBoundary checks a trust that names the
// caller's IAM role ARN is still limited by an implicit deny in that role's
// permissions boundary, while one naming a user or a role session ARN is not.
func TestEnforcedTrustRoleARNIsLimitedByBoundary(t *testing.T) {
	e := newEnforcedSTS(t)
	ctx := context.Background()
	boot := e.user("boot", "")
	admin := e.iam(boot)
	s3Only := e.policy("s3only", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)

	bndedArn := e.role("bnded", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), allowStsAR)
	if _, err := admin.PutRolePermissionsBoundary(ctx, &awsiam.PutRolePermissionsBoundaryInput{
		RoleName: aws.String("bnded"), PermissionsBoundary: aws.String(s3Only),
	}); err != nil {
		t.Fatalf("PutRolePermissionsBoundary: %v", err)
	}

	byRole := e.role("trustsrole", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(bndedArn), "")), "")
	bySession := e.role("trustssession", "", trustOf(trustStmt(`"sts:AssumeRole"`,
		awsPrincipal("arn:aws:sts::"+testAccountID+":assumed-role/bnded/s1"), "")), "")

	session, err := e.assume(boot, &awssts.AssumeRoleInput{RoleArn: aws.String(bndedArn)})
	if err != nil {
		t.Fatalf("AssumeRole bnded: %v", err)
	}

	_, err = e.assume(session, &awssts.AssumeRoleInput{RoleArn: aws.String(byRole)})
	wantAssume(t, err, false)

	_, err = e.assume(session, &awssts.AssumeRoleInput{RoleArn: aws.String(bySession)})
	wantAssume(t, err, true)

	user := e.user("caller", allowDDB)
	if _, err := admin.PutUserPermissionsBoundary(ctx, &awsiam.PutUserPermissionsBoundaryInput{
		UserName: aws.String("caller"), PermissionsBoundary: aws.String(s3Only),
	}); err != nil {
		t.Fatalf("PutUserPermissionsBoundary: %v", err)
	}

	byUser := e.role("trustsuser", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), "")), "")
	_, err = e.assume(user, &awssts.AssumeRoleInput{RoleArn: aws.String(byUser)})
	wantAssume(t, err, true)
}

// TestEnforcedTrustBoundaryUserNameClash checks a user that shares the role's
// name, and has no boundary, does not stand in for the role's boundary.
func TestEnforcedTrustBoundaryUserNameClash(t *testing.T) {
	e := newEnforcedSTS(t)
	ctx := context.Background()
	boot := e.user("boot", "")
	s3Only := e.policy("s3only", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)

	r1 := e.role("R1", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), allowStsAR)
	if _, err := e.iam(boot).PutRolePermissionsBoundary(ctx, &awsiam.PutRolePermissionsBoundaryInput{
		RoleName: aws.String("R1"), PermissionsBoundary: aws.String(s3Only),
	}); err != nil {
		t.Fatalf("PutRolePermissionsBoundary: %v", err)
	}

	t3 := e.role("T3", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(r1), "")), "")

	session, err := e.assume(boot, &awssts.AssumeRoleInput{RoleArn: aws.String(r1)})
	if err != nil {
		t.Fatalf("AssumeRole R1: %v", err)
	}

	_, err = e.assume(session, &awssts.AssumeRoleInput{RoleArn: aws.String(t3)})
	wantAssume(t, err, false)

	if _, err := e.iam(boot).CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("R1")}); err != nil {
		t.Fatalf("CreateUser R1: %v", err)
	}

	_, err = e.assume(session, &awssts.AssumeRoleInput{RoleArn: aws.String(t3)})
	wantAssume(t, err, false)
}

// TestEnforcedTrustRoleARNWithinBoundary checks a trust naming the caller's
// role ARN grants without an identity allow when the boundary allows it.
func TestEnforcedTrustRoleARNWithinBoundary(t *testing.T) {
	e := newEnforcedSTS(t)
	ctx := context.Background()
	boot := e.user("boot", "")
	stsOnly := e.policy("stsonly", allowStsAR)

	firstArn := e.role("first", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), allowDDB)
	if _, err := e.iam(boot).PutRolePermissionsBoundary(ctx, &awsiam.PutRolePermissionsBoundaryInput{
		RoleName: aws.String("first"), PermissionsBoundary: aws.String(stsOnly),
	}); err != nil {
		t.Fatalf("PutRolePermissionsBoundary: %v", err)
	}

	next := e.role("next", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(firstArn), "")), "")

	session, err := e.assume(boot, &awssts.AssumeRoleInput{RoleArn: aws.String(firstArn)})
	if err != nil {
		t.Fatalf("AssumeRole first: %v", err)
	}

	_, err = e.assume(session, &awssts.AssumeRoleInput{RoleArn: aws.String(next)})
	wantAssume(t, err, true)
}

// TestEnforcedTrustExternalIdGuard covers the confused-deputy guard: a Deny
// with StringNotEquals on sts:ExternalId applies when the caller sends none.
func TestEnforcedTrustExternalIdGuard(t *testing.T) {
	e := newEnforcedSTS(t)
	bob := e.user("caller", allowDDB)
	roleArn := e.role("guarded", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), ""),
		`{"Effect":"Deny","Principal":{"AWS":"*"},"Action":"sts:AssumeRole",`+
			`"Condition":{"StringNotEquals":{"sts:ExternalId":"x1"}}}`), "")

	_, err := e.assume(bob, &awssts.AssumeRoleInput{RoleArn: aws.String(roleArn)})
	wantAssume(t, err, false)

	_, err = e.assume(bob, &awssts.AssumeRoleInput{RoleArn: aws.String(roleArn), ExternalId: aws.String("x2")})
	wantAssume(t, err, false)

	_, err = e.assume(bob, &awssts.AssumeRoleInput{RoleArn: aws.String(roleArn), ExternalId: aws.String("x1")})
	wantAssume(t, err, true)
}

// TestEnforcedTrustRecreatedUser checks a user deleted and created again under
// the same name is not trusted by a policy saved against the old user.
func TestEnforcedTrustRecreatedUser(t *testing.T) {
	e := newEnforcedSTS(t)
	ctx := context.Background()
	e.user("caller", "")
	roleArn := e.role("named", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), "")), "")

	ak, err := e.cloud.IAM.ListAccessKeys(ctx, "caller")
	if err != nil {
		t.Fatalf("ListAccessKeys: %v", err)
	}

	for _, k := range ak {
		if err := e.cloud.IAM.DeleteAccessKey(ctx, "caller", k.AccessKeyID); err != nil {
			t.Fatalf("DeleteAccessKey: %v", err)
		}
	}

	if err := e.cloud.IAM.DeleteUser(ctx, "caller"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	again := e.user("caller", allowDDB)

	_, err = e.assume(again, &awssts.AssumeRoleInput{RoleArn: aws.String(roleArn)})
	wantAssume(t, err, false)
}

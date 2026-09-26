package ssm_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func putString(t *testing.T, client *awsssm.Client, in *awsssm.PutParameterInput) {
	t.Helper()

	if in.Type == "" && !aws.ToBool(in.Overwrite) {
		in.Type = ssmtypes.ParameterTypeString
	}

	if _, err := client.PutParameter(context.Background(), in); err != nil {
		t.Fatalf("PutParameter(%s): %v", aws.ToString(in.Name), err)
	}
}

func TestSDKLabelParameterVersionMissingVersion(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	putString(t, client, &awsssm.PutParameterInput{Name: aws.String("/lbl/p"), Value: aws.String("v")})

	_, err := client.LabelParameterVersion(ctx, &awsssm.LabelParameterVersionInput{
		Name: aws.String("/lbl/p"), ParameterVersion: aws.Int64(99), Labels: []string{"prod"},
	})
	if code := ssmErrorCode(t, err); code != "ParameterVersionNotFound" {
		t.Errorf("missing version: code = %q, want ParameterVersionNotFound", code)
	}

	_, err = client.LabelParameterVersion(ctx, &awsssm.LabelParameterVersionInput{
		Name: aws.String("/lbl/none"), ParameterVersion: aws.Int64(1), Labels: []string{"prod"},
	})
	if code := ssmErrorCode(t, err); code != "ParameterNotFound" {
		t.Errorf("missing parameter: code = %q, want ParameterNotFound", code)
	}
}

func countKeys(t *testing.T, kmsClient *awskms.Client) int {
	t.Helper()

	out, err := kmsClient.ListKeys(context.Background(), &awskms.ListKeysInput{})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	return len(out.Keys)
}

func TestSDKPutParameterUnknownKeyIDIsInvalidKeyID(t *testing.T) {
	client, kmsClient := newSSMAndKMSClients(t)
	ctx := context.Background()

	before := countKeys(t, kmsClient)

	for _, keyID := range []string{"bogus", "alias/nope", "arn:aws:kms:us-east-1:123456789012:key/0000"} {
		_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name: aws.String("/kid/p"), Value: aws.String("v"),
			Type: ssmtypes.ParameterTypeSecureString, KeyId: aws.String(keyID),
		})
		if code := apiErrorCode(t, err); code != "InvalidKeyId" {
			t.Errorf("KeyId %q: code = %q, want InvalidKeyId", keyID, code)
		}
	}

	if after := countKeys(t, kmsClient); after != before {
		t.Errorf("KMS key count = %d after bad KeyIds, want %d (no key minted)", after, before)
	}

	// The reserved AWS-managed alias still works, in both forms.
	for i, keyID := range []string{"alias/aws/ssm", "arn:aws:kms:us-east-1:123456789012:alias/aws/ssm"} {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name: aws.String(fmt.Sprintf("/kid/ok%d", i)), Value: aws.String("v"),
			Type: ssmtypes.ParameterTypeSecureString, KeyId: aws.String(keyID),
		}); err != nil {
			t.Errorf("KeyId %q: %v", keyID, err)
		}
	}
}

func TestSDKPutParameterNameTrimAndQualified(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	putString(t, client, &awsssm.PutParameterInput{Name: aws.String("  /trim/p  "), Value: aws.String("v")})

	if _, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/trim/p")}); err != nil {
		t.Fatalf("GetParameter(trimmed name): %v", err)
	}

	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("trim/p"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
	})
	if code := ssmErrorCode(t, err); code != "ValidationException" {
		t.Errorf("unqualified hierarchy: code = %q, want ValidationException", code)
	}
}

func TestSDKPutParameterNameLengthIncludesARN(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	putString(t, client, &awsssm.PutParameterInput{Name: aws.String("/x"), Value: aws.String("v")})

	got, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/x")})
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	// The ARN is "<prefix>/x", so the prefix before the name is len(ARN)-2.
	prefixLen := len(aws.ToString(got.Parameter.ARN)) - len("/x")

	fits := "/" + strings.Repeat("a", 1011-prefixLen-1)
	putString(t, client, &awsssm.PutParameterInput{Name: aws.String(fits), Value: aws.String("v")})

	_, err = client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String(fits + "b"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
	})
	if code := ssmErrorCode(t, err); code != "ValidationException" {
		t.Errorf("1012 chars with ARN: code = %q, want ValidationException", code)
	}
}

func TestSDKGetParametersElevenNamesMessage(t *testing.T) {
	client := newSSMClient(t)

	names := make([]string, 11)
	for i := range names {
		names[i] = fmt.Sprintf("/n/%d", i)
	}

	_, err := client.GetParameters(context.Background(), &awsssm.GetParametersInput{Names: names})
	if code := ssmErrorCode(t, err); code != "ValidationException" {
		t.Fatalf("code = %q, want ValidationException", code)
	}

	if !strings.Contains(err.Error(), "Value '[/n/0, /n/1") ||
		!strings.Contains(err.Error(), "Member must have length less than or equal to 10") {
		t.Errorf("message = %q, want the AWS constraint message", err.Error())
	}
}

func TestSDKOverwriteDescriptionPresence(t *testing.T) {
	client := newSSMClient(t)

	const name = "/desc/p"

	putString(t, client, &awsssm.PutParameterInput{
		Name: aws.String(name), Value: aws.String("v1"), Description: aws.String("first"),
	})

	steps := []struct {
		desc *string
		want string
	}{
		{desc: nil, want: "first"},
		{desc: aws.String("second"), want: "second"},
		{desc: nil, want: "second"},
		{desc: aws.String(""), want: ""},
	}

	for i, s := range steps {
		putString(t, client, &awsssm.PutParameterInput{
			Name: aws.String(name), Value: aws.String(fmt.Sprintf("v%d", i+2)),
			Overwrite: aws.Bool(true), Description: s.desc,
		})

		if got := aws.ToString(describeOne(t, client, name).Description); got != s.want {
			t.Errorf("step %d: Description = %q, want %q", i, got, s.want)
		}
	}
}

func TestSDKOverwriteKeepsKeyIDAndDataType(t *testing.T) {
	client, kmsClient := newSSMAndKMSClients(t)
	ctx := context.Background()

	key, err := kmsClient.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)

	putString(t, client, &awsssm.PutParameterInput{
		Name: aws.String("/keep/secure"), Value: aws.String("s1"),
		Type: ssmtypes.ParameterTypeSecureString, KeyId: aws.String(keyID),
	})
	putString(t, client, &awsssm.PutParameterInput{
		Name: aws.String("/keep/secure"), Value: aws.String("s2"), Overwrite: aws.Bool(true),
	})

	if got := aws.ToString(describeOne(t, client, "/keep/secure").KeyId); got != keyID {
		t.Errorf("KeyId after overwrite = %q, want %q", got, keyID)
	}

	putString(t, client, &awsssm.PutParameterInput{
		Name: aws.String("/keep/ami"), Value: aws.String("ami-12345678"), DataType: aws.String("aws:ec2:image"),
	})
	putString(t, client, &awsssm.PutParameterInput{
		Name: aws.String("/keep/ami"), Value: aws.String("ami-87654321"), Overwrite: aws.Bool(true),
	})

	if got := aws.ToString(describeOne(t, client, "/keep/ami").DataType); got != "aws:ec2:image" {
		t.Errorf("DataType after overwrite = %q, want aws:ec2:image", got)
	}
}

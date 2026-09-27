package ssm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func expirationIn(d time.Duration) string {
	return `{"Type":"Expiration","Version":"1.0","Attributes":{"Timestamp":"` +
		time.Now().Add(d).UTC().Format(time.RFC3339) + `"}}`
}

func TestSDKPutParameterPolicyErrors(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	put := func(tier ssmtypes.ParameterTier, pols string) error {
		_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name: aws.String("/pol/err"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
			Tier: tier, Policies: aws.String(pols),
		})

		return err
	}

	var typeErr *ssmtypes.InvalidPolicyTypeException
	if err := put(ssmtypes.ParameterTierAdvanced, `[{"Type":"Nope","Version":"1.0","Attributes":{}}]`); !errors.As(err, &typeErr) {
		t.Errorf("unknown type: err = %v, want InvalidPolicyTypeException", err)
	}

	var attrErr *ssmtypes.InvalidPolicyAttributeException
	if err := put(ssmtypes.ParameterTierAdvanced,
		`[{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"0","Unit":"Days"}}]`); !errors.As(err, &attrErr) {
		t.Errorf("bad attribute: err = %v, want InvalidPolicyAttributeException", err)
	}

	var incompatible *ssmtypes.IncompatiblePolicyException
	exp := expirationIn(time.Hour)
	if err := put(ssmtypes.ParameterTierAdvanced, "["+exp+","+exp+"]"); !errors.As(err, &incompatible) {
		t.Errorf("two expirations: err = %v, want IncompatiblePolicyException", err)
	}

	var limit *ssmtypes.PoliciesLimitExceededException
	many := "["
	for i := range 11 {
		if i > 0 {
			many += ","
		}

		many += `{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"1","Unit":"Hours"}}`
	}

	if err := put(ssmtypes.ParameterTierAdvanced, many+"]"); !errors.As(err, &limit) {
		t.Errorf("eleven policies: err = %v, want PoliciesLimitExceededException", err)
	}

	if code := ssmErrorCode(t, put(ssmtypes.ParameterTierStandard, "["+exp+"]")); code != "ValidationException" {
		t.Errorf("Standard tier with policies: code = %q, want ValidationException", code)
	}
}

func TestSDKParameterPoliciesReadBack(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	put, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/pol/read"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
		Tier:     ssmtypes.ParameterTierIntelligentTiering,
		Policies: aws.String("[" + expirationIn(time.Hour) + "]"),
	})
	if err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if put.Tier != ssmtypes.ParameterTierAdvanced {
		t.Fatalf("Intelligent-Tiering with a policy: Tier = %q, want Advanced", put.Tier)
	}

	putString(t, client, &awsssm.PutParameterInput{Name: aws.String("/pol/plain"), Value: aws.String("v")})

	desc, err := client.DescribeParameters(ctx, &awsssm.DescribeParametersInput{})
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	for _, md := range desc.Parameters {
		switch aws.ToString(md.Name) {
		case "/pol/read":
			if len(md.Policies) != 1 || aws.ToString(md.Policies[0].PolicyType) != "Expiration" ||
				aws.ToString(md.Policies[0].PolicyStatus) != "Pending" || aws.ToString(md.Policies[0].PolicyText) == "" {
				t.Errorf("described policies = %+v", md.Policies)
			}
		case "/pol/plain":
			if len(md.Policies) != 0 {
				t.Errorf("plain parameter policies = %+v, want none", md.Policies)
			}
		}
	}

	hist, err := client.GetParameterHistory(ctx, &awsssm.GetParameterHistoryInput{Name: aws.String("/pol/read")})
	if err != nil || len(hist.Parameters) != 1 || len(hist.Parameters[0].Policies) != 1 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
}

func TestSDKServiceSettings(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	const id = "/ssm/parameter-store/default-parameter-tier"

	got, err := client.GetServiceSetting(ctx, &awsssm.GetServiceSettingInput{SettingId: aws.String(id)})
	if err != nil {
		t.Fatalf("GetServiceSetting: %v", err)
	}

	s := got.ServiceSetting
	if aws.ToString(s.SettingValue) != "Standard" || aws.ToString(s.Status) != "Default" ||
		aws.ToString(s.SettingId) != id || s.LastModifiedDate == nil {
		t.Fatalf("default setting = %+v", s)
	}

	if _, err := client.UpdateServiceSetting(ctx, &awsssm.UpdateServiceSettingInput{
		SettingId: s.ARN, SettingValue: aws.String("Advanced"),
	}); err != nil {
		t.Fatalf("UpdateServiceSetting: %v", err)
	}

	// An omitted Tier now defaults to Advanced.
	put, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/pol/default"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
	})
	if err != nil || put.Tier != ssmtypes.ParameterTierAdvanced {
		t.Fatalf("put with default tier Advanced = %+v, %v", put, err)
	}

	reset, err := client.ResetServiceSetting(ctx, &awsssm.ResetServiceSettingInput{SettingId: aws.String(id)})
	if err != nil || aws.ToString(reset.ServiceSetting.SettingValue) != "Standard" {
		t.Fatalf("ResetServiceSetting = %+v, %v", reset, err)
	}

	var notFound *ssmtypes.ServiceSettingNotFound

	_, err = client.GetServiceSetting(ctx, &awsssm.GetServiceSettingInput{SettingId: aws.String("/ssm/nope")})
	if !errors.As(err, &notFound) {
		t.Fatalf("unknown setting: err = %v, want ServiceSettingNotFound", err)
	}
}

package ssm_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
)

func ssmErrorCode(t *testing.T, err error) string {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want an API error, got %v", err)
	}

	return apiErr.ErrorCode()
}

func TestSDKPutParameterNameRules(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	tests := []struct {
		name string
		want string
	}{
		{name: "invalid name with spaces!", want: "ValidationException"},
		{name: strings.Repeat("/l", 16), want: "HierarchyLevelLimitExceededException"},
	}

	for _, tc := range tests {
		_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name: aws.String(tc.name), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
		})
		if code := ssmErrorCode(t, err); code != tc.want {
			t.Errorf("PutParameter(%q): code = %q, want %s", tc.name, code, tc.want)
		}
	}

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/app/db-host.v_1"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("valid PutParameter: %v", err)
	}
}

func TestSDKBatchParametersRejectElevenNames(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	names := make([]string, 11)
	for i := range names {
		names[i] = fmt.Sprintf("/n/%d", i)
	}

	_, err := client.GetParameters(ctx, &awsssm.GetParametersInput{Names: names})
	if code := ssmErrorCode(t, err); code != "ValidationException" {
		t.Errorf("GetParameters(11): code = %q, want ValidationException", code)
	}

	_, err = client.DeleteParameters(ctx, &awsssm.DeleteParametersInput{Names: names})
	if code := ssmErrorCode(t, err); code != "ValidationException" {
		t.Errorf("DeleteParameters(11): code = %q, want ValidationException", code)
	}
}

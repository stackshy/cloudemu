package redshift_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/smithy-go"
)

// TestSDKRedshiftClusterParameters proves the parameter-group parameter surface
// real Terraform aws_redshift_parameter_group relies on: DescribeClusterParameters
// returns the family defaults, ModifyClusterParameterGroup applies overrides
// (flipping Source to "user"), and an unknown parameter is rejected with
// InvalidParameterValue. Before this, both actions returned InvalidAction 400.
func TestSDKRedshiftClusterParameters(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
		ParameterGroupName:   aws.String("pg1"),
		ParameterGroupFamily: aws.String("redshift-1.0"),
		Description:          aws.String("my pg"),
	}); err != nil {
		t.Fatalf("CreateClusterParameterGroup: %v", err)
	}

	// Defaults are readable and non-empty, all engine-default sourced.
	desc, err := client.DescribeClusterParameters(ctx, &awsredshift.DescribeClusterParametersInput{
		ParameterGroupName: aws.String("pg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterParameters: %v", err)
	}
	if len(desc.Parameters) == 0 {
		t.Fatal("expected non-empty default parameters")
	}
	if !hasParam(desc.Parameters, "require_ssl") {
		t.Fatal("expected require_ssl among defaults")
	}
	for _, p := range desc.Parameters {
		if aws.ToString(p.Source) != "engine-default" {
			t.Fatalf("param %q Source = %q, want engine-default", aws.ToString(p.ParameterName), aws.ToString(p.Source))
		}
	}

	// Apply an override; Source flips to user and value reflects.
	if _, err := client.ModifyClusterParameterGroup(ctx, &awsredshift.ModifyClusterParameterGroupInput{
		ParameterGroupName: aws.String("pg1"),
		Parameters: []redshifttypes.Parameter{
			{ParameterName: aws.String("require_ssl"), ParameterValue: aws.String("true")},
		},
	}); err != nil {
		t.Fatalf("ModifyClusterParameterGroup: %v", err)
	}

	after, err := client.DescribeClusterParameters(ctx, &awsredshift.DescribeClusterParametersInput{
		ParameterGroupName: aws.String("pg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterParameters(after): %v", err)
	}
	rs := findParam(after.Parameters, "require_ssl")
	if rs == nil || aws.ToString(rs.ParameterValue) != "true" || aws.ToString(rs.Source) != "user" {
		t.Fatalf("require_ssl after modify = %+v, want value=true source=user", rs)
	}

	// An unknown parameter is rejected with InvalidParameterValue.
	_, err = client.ModifyClusterParameterGroup(ctx, &awsredshift.ModifyClusterParameterGroupInput{
		ParameterGroupName: aws.String("pg1"),
		Parameters: []redshifttypes.Parameter{
			{ParameterName: aws.String("not_a_real_param"), ParameterValue: aws.String("x")},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown parameter")
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("unknown-parameter ErrorCode = %v, want InvalidParameterValue", err)
	}
}

func hasParam(params []redshifttypes.Parameter, name string) bool {
	return findParam(params, name) != nil
}

func findParam(params []redshifttypes.Parameter, name string) *redshifttypes.Parameter {
	for i := range params {
		if aws.ToString(params[i].ParameterName) == name {
			return &params[i]
		}
	}

	return nil
}

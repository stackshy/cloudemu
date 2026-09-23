package emr_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/emr"
	emrtypes "github.com/aws/aws-sdk-go-v2/service/emr/types"
)

func TestSDKAutoTerminationPolicyLifecycle(t *testing.T) {
	c := newEMRClient(t)
	ctx := context.Background()
	id := runCluster(t, c)

	got, err := c.GetAutoTerminationPolicy(ctx, &emr.GetAutoTerminationPolicyInput{ClusterId: aws.String(id)})
	if err != nil || got.AutoTerminationPolicy != nil {
		t.Fatalf("get before put = %+v, %v; want no policy", got, err)
	}

	if _, err = c.PutAutoTerminationPolicy(ctx, &emr.PutAutoTerminationPolicyInput{
		ClusterId: aws.String(id), AutoTerminationPolicy: &emrtypes.AutoTerminationPolicy{IdleTimeout: aws.Int64(3600)},
	}); err != nil {
		t.Fatalf("PutAutoTerminationPolicy: %v", err)
	}

	got, err = c.GetAutoTerminationPolicy(ctx, &emr.GetAutoTerminationPolicyInput{ClusterId: aws.String(id)})
	if err != nil || got.AutoTerminationPolicy == nil || aws.ToInt64(got.AutoTerminationPolicy.IdleTimeout) != 3600 {
		t.Fatalf("get after put = %+v, %v; want IdleTimeout 3600", got.AutoTerminationPolicy, err)
	}

	_, err = c.PutAutoTerminationPolicy(ctx, &emr.PutAutoTerminationPolicyInput{
		ClusterId: aws.String(id), AutoTerminationPolicy: &emrtypes.AutoTerminationPolicy{IdleTimeout: aws.Int64(10)},
	})
	requireEMRCode(t, err, "ValidationException")

	if _, err = c.RemoveAutoTerminationPolicy(ctx, &emr.RemoveAutoTerminationPolicyInput{ClusterId: aws.String(id)}); err != nil {
		t.Fatalf("RemoveAutoTerminationPolicy: %v", err)
	}

	got, err = c.GetAutoTerminationPolicy(ctx, &emr.GetAutoTerminationPolicyInput{ClusterId: aws.String(id)})
	if err != nil || got.AutoTerminationPolicy != nil {
		t.Fatalf("get after remove = %+v, %v; want no policy", got.AutoTerminationPolicy, err)
	}

	_, err = c.GetAutoTerminationPolicy(ctx, &emr.GetAutoTerminationPolicyInput{ClusterId: aws.String("j-missing")})
	requireEMRCode(t, err, "InvalidRequestException")
}

func TestSDKStepConcurrencyLevel(t *testing.T) {
	c := newEMRClient(t)
	ctx := context.Background()
	id := runCluster(t, c)

	desc, err := c.DescribeCluster(ctx, &emr.DescribeClusterInput{ClusterId: aws.String(id)})
	if err != nil || aws.ToInt32(desc.Cluster.StepConcurrencyLevel) != 1 {
		t.Fatalf("default StepConcurrencyLevel = %v, %v; want 1", desc.Cluster.StepConcurrencyLevel, err)
	}

	out, err := c.RunJobFlow(ctx, &emr.RunJobFlowInput{
		Name:                 aws.String("conc"),
		Instances:            &emrtypes.JobFlowInstancesConfig{InstanceCount: aws.Int32(1), MasterInstanceType: aws.String("m5.xlarge")},
		StepConcurrencyLevel: aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("RunJobFlow: %v", err)
	}

	desc, err = c.DescribeCluster(ctx, &emr.DescribeClusterInput{ClusterId: out.JobFlowId})
	if err != nil || aws.ToInt32(desc.Cluster.StepConcurrencyLevel) != 5 {
		t.Fatalf("StepConcurrencyLevel = %v, %v; want 5", desc.Cluster.StepConcurrencyLevel, err)
	}

	_, err = c.RunJobFlow(ctx, &emr.RunJobFlowInput{
		Name:                 aws.String("bad"),
		Instances:            &emrtypes.JobFlowInstancesConfig{InstanceCount: aws.Int32(1), MasterInstanceType: aws.String("m5.xlarge")},
		StepConcurrencyLevel: aws.Int32(0),
	})
	requireEMRCode(t, err, "ValidationException")
}

func TestSDKRunJobFlowAutoTerminationPolicy(t *testing.T) {
	c := newEMRClient(t)
	ctx := context.Background()

	out, err := c.RunJobFlow(ctx, &emr.RunJobFlowInput{
		Name:                  aws.String("idle"),
		Instances:             &emrtypes.JobFlowInstancesConfig{InstanceCount: aws.Int32(1), MasterInstanceType: aws.String("m5.xlarge")},
		AutoTerminationPolicy: &emrtypes.AutoTerminationPolicy{IdleTimeout: aws.Int64(900)},
	})
	if err != nil {
		t.Fatalf("RunJobFlow: %v", err)
	}

	got, err := c.GetAutoTerminationPolicy(ctx, &emr.GetAutoTerminationPolicyInput{ClusterId: out.JobFlowId})
	if err != nil || got.AutoTerminationPolicy == nil || aws.ToInt64(got.AutoTerminationPolicy.IdleTimeout) != 900 {
		t.Fatalf("GetAutoTerminationPolicy = %+v, %v; want 900", got, err)
	}
}

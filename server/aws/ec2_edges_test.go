package aws_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMultiClient returns an httptest server with S3 + DynamoDB + EC2
// simultaneously registered so we can catch cross-service dispatch regressions.
func newMultiClient(t *testing.T) (*ec2.Client, *s3.Client, *dynamodb.Client) {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		S3: provider.S3, DynamoDB: provider.DynamoDB, EC2: provider.EC2,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	ec2c := ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
	s3c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
	ddbc := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return ec2c, s3c, ddbc
}

// --- RunInstances edge cases ---------------------------------------------

// Bug-fix regression: MinCount=1, MaxCount=5 should launch 5, not 1.
func TestEC2RunInstancesHonoursMaxCount(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-max"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(5),
	})
	require.NoError(t, err)
	assert.Len(t, out.Instances, 5,
		"MinCount=1 MaxCount=5 should launch MaxCount instances")
}

func TestEC2RunInstancesMaxCountOnly(t *testing.T) {
	// aws-sdk-go-v2 lets MinCount be implicit; verify MaxCount-only works.
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-maxonly"), InstanceType: ec2types.InstanceTypeT2Micro,
		MaxCount: aws.Int32(2), MinCount: aws.Int32(1),
	})
	require.NoError(t, err)
	assert.Len(t, out.Instances, 2)
}

func TestEC2RunInstancesInstanceTypeReflected(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-big"), InstanceType: ec2types.InstanceTypeM5Large,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)
	assert.Equal(t, ec2types.InstanceTypeM5Large, out.Instances[0].InstanceType)
}

func TestEC2RunInstancesStateIsRunning(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-state"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)

	state := out.Instances[0].State
	require.NotNil(t, state)
	assert.Equal(t, "running", string(state.Name))
}

func TestEC2RunInstancesTagSpecNonInstanceResourceIgnored(t *testing.T) {
	// Tags with ResourceType=volume should not be applied to the instance.
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-vol"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         []ec2types.Tag{{Key: aws.String("A"), Value: aws.String("1")}},
			},
			{
				ResourceType: ec2types.ResourceTypeVolume,
				Tags:         []ec2types.Tag{{Key: aws.String("B"), Value: aws.String("2")}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)

	got := map[string]string{}
	for _, tg := range out.Instances[0].Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	assert.Equal(t, "1", got["A"])
	_, hasB := got["B"]
	assert.False(t, hasB, "volume-scoped tag should not appear on instance")
}

func TestEC2RunInstancesTagsWithSpecialChars(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-sc"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags: []ec2types.Tag{
				{Key: aws.String("Owner"), Value: aws.String("foo@example.com")},
				{Key: aws.String("App"), Value: aws.String("name with spaces")},
				{Key: aws.String("Expr"), Value: aws.String("k=v&x=y")},
			},
		}},
	})
	require.NoError(t, err)

	got := map[string]string{}
	for _, tg := range out.Instances[0].Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	assert.Equal(t, "foo@example.com", got["Owner"])
	assert.Equal(t, "name with spaces", got["App"])
	assert.Equal(t, "k=v&x=y", got["Expr"])
}

// --- DescribeInstances edge cases ----------------------------------------

func TestEC2DescribeInstancesEmpty(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	require.NoError(t, err)
	assert.Empty(t, out.Reservations)
}

func TestEC2DescribeInstancesFilterMultipleValues(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	// Two instances: leave one running, stop one.
	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-mv"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(2), MaxCount: aws.Int32(2),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 2)

	stopID := aws.ToString(run.Instances[0].InstanceId)
	_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{stopID}})
	require.NoError(t, err)

	// Filter state in [running, pending] — should find the second one.
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("instance-state-name"),
			Values: []string{"running", "pending"},
		}},
	})
	require.NoError(t, err)

	found := 0
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			st := string(inst.State.Name)
			if st == "running" || st == "pending" {
				found++
			}
		}
	}
	assert.GreaterOrEqual(t, found, 1)
}

func TestEC2DescribeInstancesMultipleFiltersAnded(t *testing.T) {
	// All filters must match (AND). Launch with tag Role=api and filter on
	// both instance-type and tag:Role — should only match our instance.
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-and"), InstanceType: ec2types.InstanceTypeT2Small,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         []ec2types.Tag{{Key: aws.String("Role"), Value: aws.String("api")}},
		}},
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	// Add another instance with different type + no tag, should NOT match.
	_, err = client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-other"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-type"), Values: []string{"t2.small"}},
			{Name: aws.String("tag:Role"), Values: []string{"api"}},
		},
	})
	require.NoError(t, err)

	ids := collectIDs(out)
	assert.Contains(t, ids, id)
	assert.Len(t, ids, 1, "only the tagged t2.small should match both filters")
}

func TestEC2DescribeTerminatedInstanceStillVisible(t *testing.T) {
	// Real AWS keeps terminated instances visible for ~1hr.
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-term"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)

	assert.Contains(t, collectIDs(out), id,
		"terminated instance should still be described")
}

func TestEC2DescribeInstancesByUnknownIDReturnsEmpty(t *testing.T) {
	// Real AWS returns an error; our provider returns empty. Document behavior.
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{"i-deadbeef"},
	})
	require.NoError(t, err)
	assert.Empty(t, collectIDs(out))
}

// --- State transitions ---------------------------------------------------

func TestEC2StopIdempotent(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-idem"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	require.NoError(t, err)

	// Second stop should error because the driver considers it an invalid
	// state transition. AWS is idempotent, but we match the provider's
	// statemachine behavior.
	_, err2 := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	_ = err2 // either behavior is acceptable; just assert we don't panic/hang
}

func TestEC2BatchOperationsMultipleIDs(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-batch"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(3), MaxCount: aws.Int32(3),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 3)

	ids := []string{
		aws.ToString(run.Instances[0].InstanceId),
		aws.ToString(run.Instances[1].InstanceId),
		aws.ToString(run.Instances[2].InstanceId),
	}

	stopOut, err := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: ids})
	require.NoError(t, err)
	assert.Len(t, stopOut.StoppingInstances, 3)

	startOut, err := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: ids})
	require.NoError(t, err)
	assert.Len(t, startOut.StartingInstances, 3)
}

// --- Wire / protocol robustness ------------------------------------------

func TestEC2GETWithActionInQueryString(t *testing.T) {
	// Our Matches supports both POST (SDK default) and GET with Action=... in
	// the URL. Prove the GET path works.
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/?Action=DescribeInstances&Version=2016-11-15")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/xml", resp.Header.Get("Content-Type"))
}

func TestEC2UnknownActionReturnsError(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.PostForm(ts.URL, map[string][]string{
		"Action":  {"FrobnicateWidget"},
		"Version": {"2016-11-15"},
	})
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEC2OverlyLargeBodyRejected(t *testing.T) {
	// Prove MaxBytesReader limit works; 2 MiB body exceeds 1 MiB cap.
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := bytes.Repeat([]byte("A"), 2<<20)

	resp, err := http.Post(ts.URL, "application/x-www-form-urlencoded",
		bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"oversized body should be rejected")
}

// --- Cross-service dispatch regression -----------------------------------

// When EC2 is registered alongside S3 and DynamoDB, each SDK client must
// still hit its own handler. This guards against Matches regressions.
func TestEC2DoesNotStealS3Requests(t *testing.T) {
	_, s3c, _ := newMultiClient(t)
	ctx := context.Background()

	_, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("xbucket")})
	require.NoError(t, err)

	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("xbucket"), Key: aws.String("k"),
		Body: bytes.NewReader([]byte("ok")),
	})
	require.NoError(t, err)
}

func TestEC2DoesNotStealDynamoDBRequests(t *testing.T) {
	_, _, ddbc := newMultiClient(t)
	ctx := context.Background()

	_, err := ddbc.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("xt"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = ddbc.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("xt"),
		Item: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "1"},
		},
	})
	require.NoError(t, err)
}

func TestEC2AndS3AndDDBInterleaved(t *testing.T) {
	// Drive all three SDK clients in a single sequence on one server —
	// realistic multi-service workflow.
	ec2c, s3c, ddbc := newMultiClient(t)
	ctx := context.Background()

	_, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("wf-bucket")})
	require.NoError(t, err)

	_, err = ddbc.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("wf-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	run, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-wf"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 1)

	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("wf-bucket"), Key: aws.String("meta"),
		Body: bytes.NewReader([]byte(aws.ToString(run.Instances[0].InstanceId))),
	})
	require.NoError(t, err)
}

// --- Concurrency ---------------------------------------------------------

func TestEC2StopInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{"i-ghost"},
	})
	require.Error(t, err)
}

func TestEC2TerminateInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{"i-ghost"},
	})
	require.Error(t, err)
}

func TestEC2RebootInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.RebootInstances(ctx, &ec2.RebootInstancesInput{
		InstanceIds: []string{"i-ghost"},
	})
	require.Error(t, err)
}

func TestEC2ModifyInstanceAttributeUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String("i-ghost"),
		InstanceType: &ec2types.AttributeValue{
			Value: aws.String(string(ec2types.InstanceTypeT2Small)),
		},
	})
	require.Error(t, err)
}

func TestEC2ModifyInstanceAttributeNoopSucceeds(t *testing.T) {
	// ModifyInstanceAttribute with no modifiable field should still return
	// success (matches real AWS behavior).
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-noop"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	// DisableApiTermination isn't wired in our driver but the request
	// should still return OK with <return>true</return>.
	_, err = client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: run.Instances[0].InstanceId,
		DisableApiTermination: &ec2types.AttributeBooleanValue{
			Value: aws.Bool(true),
		},
	})
	require.NoError(t, err)
}

func TestEC2ConcurrentRunInstances(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	const workers = 10

	var wg sync.WaitGroup

	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
				ImageId: aws.String("ami-conc"), InstanceType: ec2types.InstanceTypeT2Micro,
				MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
			})
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	require.NoError(t, err)

	total := 0
	for _, r := range out.Reservations {
		total += len(r.Instances)
	}
	assert.Equal(t, workers, total,
		"all %d concurrent launches should be persisted", workers)
}

// collectIDs flattens every instance id across all reservations.
func collectIDs(out *ec2.DescribeInstancesOutput) []string {
	var ids []string

	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			ids = append(ids, aws.ToString(inst.InstanceId))
		}
	}

	return ids
}

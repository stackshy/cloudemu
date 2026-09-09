package globalaccelerator_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsga "github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsga.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{GlobalAccelerator: cloud.GlobalAccelerator})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-west-2"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsga.NewFromConfig(cfg, func(o *awsga.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKAcceleratorLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateAccelerator(ctx, &awsga.CreateAcceleratorInput{
		Name:    aws.String("web"),
		Enabled: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateAccelerator: %v", err)
	}

	acc := create.Accelerator
	arn := aws.ToString(acc.AcceleratorArn)

	if acc.Status != gatypes.AcceleratorStatusDeployed {
		t.Fatalf("create status = %q, want DEPLOYED", acc.Status)
	}

	// A global service: the ARN carries an empty region field.
	const wantPrefix = "arn:aws:globalaccelerator::123456789012:accelerator/"
	if len(arn) <= len(wantPrefix) || arn[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("accelerator arn = %q, want prefix %q", arn, wantPrefix)
	}

	dns := aws.ToString(acc.DnsName)
	if len(acc.IpSets) != 1 || len(acc.IpSets[0].IpAddresses) != 2 {
		t.Fatalf("want one IPv4 set of two addresses, got %+v", acc.IpSets)
	}

	ips := acc.IpSets[0].IpAddresses

	// Computed fields are byte-stable across a Describe read.
	desc, err := c.DescribeAccelerator(ctx, &awsga.DescribeAcceleratorInput{AcceleratorArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeAccelerator: %v", err)
	}

	if got := aws.ToString(desc.Accelerator.DnsName); got != dns {
		t.Fatalf("DnsName drift: %q != %q", got, dns)
	}

	if got := desc.Accelerator.IpSets[0].IpAddresses; got[0] != ips[0] || got[1] != ips[1] {
		t.Fatalf("IpSets drift: %v != %v", got, ips)
	}

	if !desc.Accelerator.CreatedTime.Equal(*acc.CreatedTime) {
		t.Fatalf("CreatedTime drift: %v != %v", desc.Accelerator.CreatedTime, acc.CreatedTime)
	}

	// Delete is rejected while enabled.
	_, err = c.DeleteAccelerator(ctx, &awsga.DeleteAcceleratorInput{AcceleratorArn: aws.String(arn)})
	if err == nil {
		t.Fatalf("DeleteAccelerator while enabled: want AcceleratorNotDisabledException")
	}

	var notDisabled *gatypes.AcceleratorNotDisabledException
	if !errors.As(err, &notDisabled) {
		t.Fatalf("delete-while-enabled error = %v, want AcceleratorNotDisabledException", err)
	}

	// Disable, then delete succeeds.
	if _, err = c.UpdateAccelerator(ctx, &awsga.UpdateAcceleratorInput{
		AcceleratorArn: aws.String(arn), Enabled: aws.Bool(false),
	}); err != nil {
		t.Fatalf("UpdateAccelerator disable: %v", err)
	}

	if _, err = c.DeleteAccelerator(ctx, &awsga.DeleteAcceleratorInput{AcceleratorArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteAccelerator after disable: %v", err)
	}

	_, err = c.DescribeAccelerator(ctx, &awsga.DescribeAcceleratorInput{AcceleratorArn: aws.String(arn)})

	var notFound *gatypes.AcceleratorNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("describe after delete = %v, want AcceleratorNotFoundException", err)
	}
}

func TestSDKListenerAndEndpointGroup(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	acc, err := c.CreateAccelerator(ctx, &awsga.CreateAcceleratorInput{Name: aws.String("edge")})
	if err != nil {
		t.Fatalf("CreateAccelerator: %v", err)
	}

	accArn := aws.ToString(acc.Accelerator.AcceleratorArn)

	lst, err := c.CreateListener(ctx, &awsga.CreateListenerInput{
		AcceleratorArn: aws.String(accArn),
		Protocol:       gatypes.ProtocolTcp,
		PortRanges:     []gatypes.PortRange{{FromPort: aws.Int32(80), ToPort: aws.Int32(80)}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}

	lArn := aws.ToString(lst.Listener.ListenerArn)

	// Default client affinity is NONE.
	if lst.Listener.ClientAffinity != gatypes.ClientAffinityNone {
		t.Fatalf("client affinity = %q, want NONE", lst.Listener.ClientAffinity)
	}

	// Disable the accelerator so the delete passes the disabled guard and reaches
	// the listener-association guard.
	if _, err = c.UpdateAccelerator(ctx, &awsga.UpdateAcceleratorInput{
		AcceleratorArn: aws.String(accArn), Enabled: aws.Bool(false),
	}); err != nil {
		t.Fatalf("UpdateAccelerator disable: %v", err)
	}

	// Deleting the accelerator is rejected while a listener is associated.
	_, err = c.DeleteAccelerator(ctx, &awsga.DeleteAcceleratorInput{AcceleratorArn: aws.String(accArn)})

	var assocListener *gatypes.AssociatedListenerFoundException
	if !errors.As(err, &assocListener) {
		t.Fatalf("delete accelerator with listener = %v, want AssociatedListenerFoundException", err)
	}

	eg, err := c.CreateEndpointGroup(ctx, &awsga.CreateEndpointGroupInput{
		ListenerArn:         aws.String(lArn),
		EndpointGroupRegion: aws.String("us-east-1"),
	})
	if err != nil {
		t.Fatalf("CreateEndpointGroup: %v", err)
	}

	egArn := aws.ToString(eg.EndpointGroup.EndpointGroupArn)

	// Defaults: traffic dial 100, health check TCP interval 30, threshold 3, port 80.
	if got := aws.ToFloat32(eg.EndpointGroup.TrafficDialPercentage); got != 100 {
		t.Fatalf("traffic dial = %v, want 100", got)
	}

	if eg.EndpointGroup.HealthCheckProtocol != gatypes.HealthCheckProtocolTcp {
		t.Fatalf("health check protocol = %q, want TCP", eg.EndpointGroup.HealthCheckProtocol)
	}

	if got := aws.ToInt32(eg.EndpointGroup.HealthCheckPort); got != 80 {
		t.Fatalf("health check port = %d, want 80 (default to listener port)", got)
	}

	// Deleting the listener is rejected while an endpoint group is associated.
	_, err = c.DeleteListener(ctx, &awsga.DeleteListenerInput{ListenerArn: aws.String(lArn)})

	var assocEG *gatypes.AssociatedEndpointGroupFoundException
	if !errors.As(err, &assocEG) {
		t.Fatalf("delete listener with endpoint group = %v, want AssociatedEndpointGroupFoundException", err)
	}

	// Tear down the tree bottom-up.
	if _, err = c.DeleteEndpointGroup(ctx, &awsga.DeleteEndpointGroupInput{
		EndpointGroupArn: aws.String(egArn),
	}); err != nil {
		t.Fatalf("DeleteEndpointGroup: %v", err)
	}

	if _, err = c.DeleteListener(ctx, &awsga.DeleteListenerInput{ListenerArn: aws.String(lArn)}); err != nil {
		t.Fatalf("DeleteListener: %v", err)
	}
}

func TestSDKAcceleratorAttributesAndTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	acc, err := c.CreateAccelerator(ctx, &awsga.CreateAcceleratorInput{
		Name: aws.String("flows"),
		Tags: []gatypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateAccelerator: %v", err)
	}

	arn := aws.ToString(acc.Accelerator.AcceleratorArn)

	upd, err := c.UpdateAcceleratorAttributes(ctx, &awsga.UpdateAcceleratorAttributesInput{
		AcceleratorArn:   aws.String(arn),
		FlowLogsEnabled:  aws.Bool(true),
		FlowLogsS3Bucket: aws.String("logs-bucket"),
		FlowLogsS3Prefix: aws.String("ga/"),
	})
	if err != nil {
		t.Fatalf("UpdateAcceleratorAttributes: %v", err)
	}

	if !aws.ToBool(upd.AcceleratorAttributes.FlowLogsEnabled) {
		t.Fatalf("flow logs not enabled after update")
	}

	desc, err := c.DescribeAcceleratorAttributes(ctx, &awsga.DescribeAcceleratorAttributesInput{
		AcceleratorArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeAcceleratorAttributes: %v", err)
	}

	if got := aws.ToString(desc.AcceleratorAttributes.FlowLogsS3Bucket); got != "logs-bucket" {
		t.Fatalf("flow logs bucket = %q, want logs-bucket", got)
	}

	tags, err := c.ListTagsForResource(ctx, &awsga.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("tags = %+v, want single env=prod", tags.Tags)
	}
}

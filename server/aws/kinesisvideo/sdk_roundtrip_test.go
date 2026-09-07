package kinesisvideo_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	kv "github.com/aws/aws-sdk-go-v2/service/kinesisvideo"
	kvtypes "github.com/aws/aws-sdk-go-v2/service/kinesisvideo/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *kv.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{KinesisVideo: cloud.KinesisVideo})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return kv.NewFromConfig(cfg, func(o *kv.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func describeStream(t *testing.T, c *kv.Client, name string) *kvtypes.StreamInfo {
	t.Helper()

	out, err := c.DescribeStream(context.Background(), &kv.DescribeStreamInput{StreamName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeStream(%s): %v", name, err)
	}

	return out.StreamInfo
}

func TestStreamLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "cam-feed"

	created, err := c.CreateStream(ctx, &kv.CreateStreamInput{
		StreamName:           aws.String(name),
		DataRetentionInHours: aws.Int32(24),
		MediaType:            aws.String("video/h264"),
		DeviceName:           aws.String("device-1"),
		Tags:                 map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if !strings.HasPrefix(aws.ToString(created.StreamARN), "arn:aws:kinesisvideo:us-east-1:") ||
		!strings.Contains(aws.ToString(created.StreamARN), ":stream/"+name+"/") {
		t.Fatalf("unexpected stream ARN: %q", aws.ToString(created.StreamARN))
	}

	first := describeStream(t, c, name)
	if first.Status != kvtypes.StatusActive {
		t.Fatalf("status = %q, want ACTIVE", first.Status)
	}

	if aws.ToInt32(first.DataRetentionInHours) != 24 {
		t.Fatalf("retention = %d, want 24", aws.ToInt32(first.DataRetentionInHours))
	}

	if aws.ToString(first.MediaType) != "video/h264" || aws.ToString(first.DeviceName) != "device-1" {
		t.Fatalf("media/device mismatch: %+v", first)
	}

	if first.CreationTime == nil {
		t.Fatal("CreationTime is nil")
	}

	// Computed fields are stable across repeated reads (no IaC drift).
	second := describeStream(t, c, name)
	if aws.ToString(first.StreamARN) != aws.ToString(second.StreamARN) ||
		aws.ToString(first.Version) != aws.ToString(second.Version) ||
		!first.CreationTime.Equal(*second.CreationTime) {
		t.Fatalf("computed fields drifted across reads: %+v vs %+v", first, second)
	}

	// UpdateStream rotates Version but preserves ARN and CreationTime.
	if _, err = c.UpdateStream(ctx, &kv.UpdateStreamInput{
		StreamName:     aws.String(name),
		CurrentVersion: first.Version,
		DeviceName:     aws.String("device-2"),
	}); err != nil {
		t.Fatalf("UpdateStream: %v", err)
	}

	updated := describeStream(t, c, name)
	if aws.ToString(updated.Version) == aws.ToString(first.Version) {
		t.Fatal("Version did not change after UpdateStream")
	}

	if aws.ToString(updated.StreamARN) != aws.ToString(first.StreamARN) ||
		!updated.CreationTime.Equal(*first.CreationTime) {
		t.Fatal("ARN or CreationTime drifted after UpdateStream")
	}

	if aws.ToString(updated.DeviceName) != "device-2" {
		t.Fatalf("DeviceName = %q, want device-2", aws.ToString(updated.DeviceName))
	}

	// Tagging round-trips.
	if _, err = c.TagStream(ctx, &kv.TagStreamInput{
		StreamARN: updated.StreamARN,
		Tags:      map[string]string{"team": "video"},
	}); err != nil {
		t.Fatalf("TagStream: %v", err)
	}

	tagsOut, err := c.ListTagsForStream(ctx, &kv.ListTagsForStreamInput{StreamName: aws.String(name)})
	if err != nil {
		t.Fatalf("ListTagsForStream: %v", err)
	}

	if tagsOut.Tags["env"] != "test" || tagsOut.Tags["team"] != "video" {
		t.Fatalf("tags = %v, want env=test team=video", tagsOut.Tags)
	}

	// Delete, then a describe reports ResourceNotFoundException.
	if _, err = c.DeleteStream(ctx, &kv.DeleteStreamInput{StreamARN: updated.StreamARN, CurrentVersion: updated.Version}); err != nil {
		t.Fatalf("DeleteStream: %v", err)
	}

	_, err = c.DescribeStream(ctx, &kv.DescribeStreamInput{StreamName: aws.String(name)})

	var nf *kvtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeStream after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestCreateStreamDuplicateInUse(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	in := &kv.CreateStreamInput{StreamName: aws.String("dup"), DataRetentionInHours: aws.Int32(0)}
	if _, err := c.CreateStream(ctx, in); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	_, err := c.CreateStream(ctx, in)

	var inUse *kvtypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Fatalf("duplicate CreateStream: got %v, want ResourceInUseException", err)
	}
}

func TestListStreams(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	for _, n := range []string{"alpha", "beta"} {
		if _, err := c.CreateStream(ctx, &kv.CreateStreamInput{StreamName: aws.String(n), DataRetentionInHours: aws.Int32(1)}); err != nil {
			t.Fatalf("CreateStream(%s): %v", n, err)
		}
	}

	out, err := c.ListStreams(ctx, &kv.ListStreamsInput{})
	if err != nil {
		t.Fatalf("ListStreams: %v", err)
	}

	if len(out.StreamInfoList) != 2 {
		t.Fatalf("ListStreams returned %d streams, want 2", len(out.StreamInfoList))
	}
}

func TestSignalingChannelLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "signal-1"

	created, err := c.CreateSignalingChannel(ctx, &kv.CreateSignalingChannelInput{
		ChannelName: aws.String(name),
		ChannelType: kvtypes.ChannelTypeSingleMaster,
		Tags:        []kvtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateSignalingChannel: %v", err)
	}

	if !strings.Contains(aws.ToString(created.ChannelARN), ":channel/"+name+"/") {
		t.Fatalf("unexpected channel ARN: %q", aws.ToString(created.ChannelARN))
	}

	desc, err := c.DescribeSignalingChannel(ctx, &kv.DescribeSignalingChannelInput{ChannelName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeSignalingChannel: %v", err)
	}

	info := desc.ChannelInfo
	if info.ChannelStatus != kvtypes.StatusActive || info.ChannelType != kvtypes.ChannelTypeSingleMaster {
		t.Fatalf("channel status/type mismatch: %+v", info)
	}

	// Resource-level tag read reaches the channel through the ARN-scoped path.
	tags, err := c.ListTagsForResource(ctx, &kv.ListTagsForResourceInput{ResourceARN: info.ChannelARN})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if tags.Tags["env"] != "test" {
		t.Fatalf("channel tags = %v, want env=test", tags.Tags)
	}

	if _, err = c.UpdateSignalingChannel(ctx, &kv.UpdateSignalingChannelInput{
		ChannelARN:                info.ChannelARN,
		CurrentVersion:            info.Version,
		SingleMasterConfiguration: &kvtypes.SingleMasterConfiguration{MessageTtlSeconds: aws.Int32(120)},
	}); err != nil {
		t.Fatalf("UpdateSignalingChannel: %v", err)
	}

	after, err := c.DescribeSignalingChannel(ctx, &kv.DescribeSignalingChannelInput{ChannelName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeSignalingChannel after update: %v", err)
	}

	if aws.ToInt32(after.ChannelInfo.SingleMasterConfiguration.MessageTtlSeconds) != 120 {
		t.Fatalf("message TTL = %d, want 120", aws.ToInt32(after.ChannelInfo.SingleMasterConfiguration.MessageTtlSeconds))
	}

	if aws.ToString(after.ChannelInfo.Version) == aws.ToString(info.Version) {
		t.Fatal("channel Version did not change after update")
	}

	if _, err = c.DeleteSignalingChannel(ctx, &kv.DeleteSignalingChannelInput{
		ChannelARN:     info.ChannelARN,
		CurrentVersion: after.ChannelInfo.Version,
	}); err != nil {
		t.Fatalf("DeleteSignalingChannel: %v", err)
	}

	_, err = c.DescribeSignalingChannel(ctx, &kv.DescribeSignalingChannelInput{ChannelName: aws.String(name)})

	var nf *kvtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeSignalingChannel after delete: got %v, want ResourceNotFoundException", err)
	}
}

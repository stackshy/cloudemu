// Package driver defines the interface and types for the Amazon Kinesis Video
// Streams control-plane API (restJson1). It models Kinesis video streams and
// signaling channels plus their resource tags.
//
// This is a control-plane-only surface: the emulator never ingests or serves
// video (there is no GetMedia/PutMedia data plane). A stream and a signaling
// channel are created directly in the ACTIVE state so an IaC waiter that blocks
// on status does not hang. The computed fields clients and IaC tools read back —
// the StreamARN (which embeds the creation Unix timestamp), the Version token,
// the Status and the CreationTime — are minted once at create and stored, so
// repeated DescribeStream/ListStreams reads never drift; an UpdateStream (or
// UpdateDataRetention) rotates the Version token to a fresh value, matching real
// Kinesis Video, while every other computed field stays stable.
//
// Distinct from the kinesis package (Kinesis Data Streams): that is a separate
// service with a separate wire protocol and is not touched here.
package driver

import (
	"context"
	"time"
)

// Stream / channel status values. A newly created resource is ACTIVE so an IaC
// create waiter (which blocks until status is ACTIVE) completes without a
// real-cloud provisioning wait.
const (
	StatusCreating = "CREATING"
	StatusActive   = "ACTIVE"
	StatusUpdating = "UPDATING"
	StatusDeleting = "DELETING"
)

// Signaling channel type values.
const (
	ChannelTypeSingleMaster = "SINGLE_MASTER"
	ChannelTypeFullMesh     = "FULL_MESH"
)

// ComparisonBeginsWith is the only comparison operator ListStreams /
// ListSignalingChannels name conditions support, matching real Kinesis Video.
const ComparisonBeginsWith = "BEGINS_WITH"

// StreamInfo is a Kinesis video stream. StreamARN, Version, Status and
// CreationTime are computed at create and stored; Version is rotated on every
// mutation while the other computed fields stay stable across reads.
type StreamInfo struct {
	StreamName           string
	StreamARN            string
	MediaType            string
	KmsKeyID             string
	DeviceName           string
	DataRetentionInHours int32
	Version              string
	Status               string
	CreationTime         time.Time
	Tags                 map[string]string
}

// ChannelInfo is a Kinesis video signaling channel. ChannelARN, Version,
// ChannelStatus and CreationTime are computed at create and stored; Version is
// rotated on every mutation while the other computed fields stay stable.
type ChannelInfo struct {
	ChannelName       string
	ChannelARN        string
	ChannelType       string
	ChannelStatus     string
	MessageTTLSeconds int32
	Version           string
	CreationTime      time.Time
	Tags              map[string]string
}

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken      string
	MaxResults     int32
	NameBeginsWith string
}

// CreateStreamInput is the input to CreateStream. Its JSON tags match the
// CreateStream request body exactly, so the server unmarshals the request into
// it directly.
type CreateStreamInput struct {
	StreamName           string            `json:"StreamName"`
	DeviceName           string            `json:"DeviceName"`
	MediaType            string            `json:"MediaType"`
	KmsKeyID             string            `json:"KmsKeyId"`
	DataRetentionInHours *int32            `json:"DataRetentionInHours"`
	Tags                 map[string]string `json:"Tags"`
}

// UpdateStreamInput is the input to UpdateStream. Either StreamName or StreamARN
// identifies the stream; CurrentVersion must match the stored version.
type UpdateStreamInput struct {
	StreamName     string `json:"StreamName"`
	StreamARN      string `json:"StreamARN"`
	CurrentVersion string `json:"CurrentVersion"`
	DeviceName     string `json:"DeviceName"`
	MediaType      string `json:"MediaType"`
}

// UpdateDataRetentionInput is the input to UpdateDataRetention. Operation is
// INCREASE_DATA_RETENTION or DECREASE_DATA_RETENTION.
type UpdateDataRetentionInput struct {
	StreamName                 string `json:"StreamName"`
	StreamARN                  string `json:"StreamARN"`
	CurrentVersion             string `json:"CurrentVersion"`
	Operation                  string `json:"Operation"`
	DataRetentionChangeInHours int32  `json:"DataRetentionChangeInHours"`
}

// Operation values for UpdateDataRetention.
const (
	OperationIncreaseDataRetention = "INCREASE_DATA_RETENTION"
	OperationDecreaseDataRetention = "DECREASE_DATA_RETENTION"
)

// StreamRef identifies a stream by name or ARN (exactly one is set), shared by
// DescribeStream, DeleteStream and the stream tagging operations.
type StreamRef struct {
	StreamName string
	StreamARN  string
}

// CreateChannelInput is the input to CreateSignalingChannel.
type CreateChannelInput struct {
	ChannelName               string                     `json:"ChannelName"`
	ChannelType               string                     `json:"ChannelType"`
	SingleMasterConfiguration *SingleMasterConfiguration `json:"SingleMasterConfiguration"`
	Tags                      []Tag                      `json:"Tags"`
}

// SingleMasterConfiguration is the SINGLE_MASTER channel configuration block.
type SingleMasterConfiguration struct {
	MessageTTLSeconds *int32 `json:"MessageTtlSeconds"`
}

// Tag is a Key/Value tag as carried by the resource-level tagging operations
// (TagResource / CreateSignalingChannel), which use a Tag list rather than the
// string-to-string map the stream-level operations use.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// UpdateChannelInput is the input to UpdateSignalingChannel.
type UpdateChannelInput struct {
	ChannelARN                string                     `json:"ChannelARN"`
	CurrentVersion            string                     `json:"CurrentVersion"`
	SingleMasterConfiguration *SingleMasterConfiguration `json:"SingleMasterConfiguration"`
}

// ChannelRef identifies a channel by name or ARN, shared by
// DescribeSignalingChannel and DeleteSignalingChannel.
type ChannelRef struct {
	ChannelName string
	ChannelARN  string
}

// KinesisVideo is the Amazon Kinesis Video Streams control-plane surface:
// streams, signaling channels, and their resource tags.
type KinesisVideo interface {
	CreateStream(ctx context.Context, in *CreateStreamInput) (*StreamInfo, error)
	DescribeStream(ctx context.Context, ref StreamRef) (*StreamInfo, error)
	UpdateStream(ctx context.Context, in *UpdateStreamInput) error
	UpdateDataRetention(ctx context.Context, in *UpdateDataRetentionInput) error
	DeleteStream(ctx context.Context, ref StreamRef, currentVersion string) error
	ListStreams(ctx context.Context, page Page) (streams []StreamInfo, nextToken string, err error)

	TagStream(ctx context.Context, ref StreamRef, tags map[string]string) error
	UntagStream(ctx context.Context, ref StreamRef, tagKeys []string) error
	ListTagsForStream(ctx context.Context, ref StreamRef, nextToken string) (tags map[string]string, next string, err error)

	CreateSignalingChannel(ctx context.Context, in *CreateChannelInput) (*ChannelInfo, error)
	DescribeSignalingChannel(ctx context.Context, ref ChannelRef) (*ChannelInfo, error)
	UpdateSignalingChannel(ctx context.Context, in *UpdateChannelInput) error
	DeleteSignalingChannel(ctx context.Context, arn, currentVersion string) error
	ListSignalingChannels(ctx context.Context, page Page) (channels []ChannelInfo, nextToken string, err error)

	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN, nextToken string) (tags map[string]string, next string, err error)
}

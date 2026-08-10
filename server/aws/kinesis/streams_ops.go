package kinesis

import (
	"context"
	"net/http"

	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

type createStreamRequest struct {
	StreamName        string            `json:"StreamName"`
	ShardCount        int32             `json:"ShardCount"`
	StreamModeDetails *streamModeJSON   `json:"StreamModeDetails"`
	Tags              map[string]string `json:"Tags"`
}

func (h *Handler) createStream(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createStreamRequest) (any, error) {
		mode := ""
		if req.StreamModeDetails != nil {
			mode = req.StreamModeDetails.StreamMode
		}

		err := h.kinesis.CreateStream(ctx, kinesisdriver.CreateStreamInput{
			StreamName: req.StreamName,
			ShardCount: req.ShardCount,
			StreamMode: mode,
			Tags:       req.Tags,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deleteStreamRequest struct {
	StreamName              string `json:"StreamName"`
	StreamARN               string `json:"StreamARN"`
	EnforceConsumerDeletion bool   `json:"EnforceConsumerDeletion"`
}

func (h *Handler) deleteStream(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteStreamRequest) (any, error) {
		if err := h.kinesis.DeleteStream(ctx, req.StreamName, req.StreamARN, req.EnforceConsumerDeletion); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type describeStreamRequest struct {
	StreamName            string `json:"StreamName"`
	StreamARN             string `json:"StreamARN"`
	Limit                 int32  `json:"Limit"`
	ExclusiveStartShardID string `json:"ExclusiveStartShardId"`
}

type streamDescriptionJSON struct {
	StreamName              string                `json:"StreamName"`
	StreamARN               string                `json:"StreamARN"`
	StreamStatus            string                `json:"StreamStatus"`
	StreamModeDetails       streamModeJSON        `json:"StreamModeDetails"`
	Shards                  []shardJSON           `json:"Shards"`
	HasMoreShards           bool                  `json:"HasMoreShards"`
	RetentionPeriodHours    int32                 `json:"RetentionPeriodHours"`
	StreamCreationTimestamp *float64              `json:"StreamCreationTimestamp,omitempty"`
	EnhancedMonitoring      []enhancedMetricsJSON `json:"EnhancedMonitoring"`
	EncryptionType          string                `json:"EncryptionType,omitempty"`
	KeyID                   string                `json:"KeyId,omitempty"`
}

func (h *Handler) describeStream(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeStreamRequest) (any, error) {
		d, err := h.kinesis.DescribeStream(ctx, req.StreamName, req.StreamARN, req.Limit, req.ExclusiveStartShardID)
		if err != nil {
			return nil, err
		}

		return map[string]any{"StreamDescription": streamDescriptionJSON{
			StreamName:              d.StreamName,
			StreamARN:               d.StreamARN,
			StreamStatus:            d.StreamStatus,
			StreamModeDetails:       streamModeJSON{d.StreamModeDetails},
			Shards:                  shardsToWire(d.Shards),
			HasMoreShards:           d.HasMoreShards,
			RetentionPeriodHours:    d.RetentionPeriodHours,
			StreamCreationTimestamp: epochOrNil(d.StreamCreationTimestamp),
			EnhancedMonitoring:      monitoringToWire(d.EnhancedMonitoring),
			EncryptionType:          d.EncryptionType,
			KeyID:                   d.KeyID,
		}}, nil
	})
}

type streamSummaryJSON struct {
	StreamName              string                `json:"StreamName"`
	StreamARN               string                `json:"StreamARN"`
	StreamStatus            string                `json:"StreamStatus"`
	StreamModeDetails       streamModeJSON        `json:"StreamModeDetails"`
	RetentionPeriodHours    int32                 `json:"RetentionPeriodHours"`
	StreamCreationTimestamp *float64              `json:"StreamCreationTimestamp,omitempty"`
	EnhancedMonitoring      []enhancedMetricsJSON `json:"EnhancedMonitoring"`
	EncryptionType          string                `json:"EncryptionType,omitempty"`
	KeyID                   string                `json:"KeyId,omitempty"`
	OpenShardCount          int32                 `json:"OpenShardCount"`
	ConsumerCount           int32                 `json:"ConsumerCount"`
}

func (h *Handler) describeStreamSummary(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeStreamRequest) (any, error) {
		s, err := h.kinesis.DescribeStreamSummary(ctx, req.StreamName, req.StreamARN)
		if err != nil {
			return nil, err
		}

		return map[string]any{"StreamDescriptionSummary": streamSummaryJSON{
			StreamName:              s.StreamName,
			StreamARN:               s.StreamARN,
			StreamStatus:            s.StreamStatus,
			StreamModeDetails:       streamModeJSON{s.StreamModeDetails},
			RetentionPeriodHours:    s.RetentionPeriodHours,
			StreamCreationTimestamp: epochOrNil(s.StreamCreationTimestamp),
			EnhancedMonitoring:      monitoringToWire(s.EnhancedMonitoring),
			EncryptionType:          s.EncryptionType,
			KeyID:                   s.KeyID,
			OpenShardCount:          s.OpenShardCount,
			ConsumerCount:           s.ConsumerCount,
		}}, nil
	})
}

type listStreamsRequest struct {
	Limit     int32  `json:"Limit"`
	NextToken string `json:"NextToken"`
}

func (h *Handler) listStreams(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listStreamsRequest) (any, error) {
		out, err := h.kinesis.ListStreams(ctx, req.NextToken, req.Limit)
		if err != nil {
			return nil, err
		}

		summaries := make([]streamSummaryJSON, 0, len(out.StreamSummaries))

		for i := range out.StreamSummaries {
			s := out.StreamSummaries[i]
			summaries = append(summaries, streamSummaryJSON{
				StreamName:              s.StreamName,
				StreamARN:               s.StreamARN,
				StreamStatus:            s.StreamStatus,
				StreamModeDetails:       streamModeJSON{s.StreamModeDetails},
				RetentionPeriodHours:    s.RetentionPeriodHours,
				StreamCreationTimestamp: epochOrNil(s.StreamCreationTimestamp),
				EnhancedMonitoring:      monitoringToWire(s.EnhancedMonitoring),
				EncryptionType:          s.EncryptionType,
				OpenShardCount:          s.OpenShardCount,
				ConsumerCount:           s.ConsumerCount,
			})
		}

		return map[string]any{
			"StreamNames":     out.StreamNames,
			"StreamSummaries": summaries,
			"HasMoreStreams":  out.HasMoreStreams,
		}, nil
	})
}

type retentionRequest struct {
	StreamName           string `json:"StreamName"`
	StreamARN            string `json:"StreamARN"`
	RetentionPeriodHours int32  `json:"RetentionPeriodHours"`
}

func (h *Handler) increaseRetention(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *retentionRequest) (any, error) {
		if err := h.kinesis.IncreaseStreamRetentionPeriod(ctx, req.StreamName, req.StreamARN, req.RetentionPeriodHours); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) decreaseRetention(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *retentionRequest) (any, error) {
		if err := h.kinesis.DecreaseStreamRetentionPeriod(ctx, req.StreamName, req.StreamARN, req.RetentionPeriodHours); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type updateShardCountRequest struct {
	StreamName       string `json:"StreamName"`
	StreamARN        string `json:"StreamARN"`
	TargetShardCount int32  `json:"TargetShardCount"`
	ScalingType      string `json:"ScalingType"`
}

func (h *Handler) updateShardCount(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateShardCountRequest) (any, error) {
		current, target, err := h.kinesis.UpdateShardCount(ctx, req.StreamName, req.StreamARN, req.TargetShardCount, req.ScalingType)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"StreamName":        req.StreamName,
			"CurrentShardCount": current,
			"TargetShardCount":  target,
			"StreamARN":         req.StreamARN,
		}, nil
	})
}

type updateStreamModeRequest struct {
	StreamARN         string          `json:"StreamARN"`
	StreamModeDetails *streamModeJSON `json:"StreamModeDetails"`
}

func (h *Handler) updateStreamMode(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateStreamModeRequest) (any, error) {
		mode := ""
		if req.StreamModeDetails != nil {
			mode = req.StreamModeDetails.StreamMode
		}

		if err := h.kinesis.UpdateStreamMode(ctx, req.StreamARN, mode); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type mergeShardsRequest struct {
	StreamName           string `json:"StreamName"`
	StreamARN            string `json:"StreamARN"`
	ShardToMerge         string `json:"ShardToMerge"`
	AdjacentShardToMerge string `json:"AdjacentShardToMerge"`
}

func (h *Handler) mergeShards(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *mergeShardsRequest) (any, error) {
		if err := h.kinesis.MergeShards(ctx, req.StreamName, req.StreamARN, req.ShardToMerge, req.AdjacentShardToMerge); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type splitShardRequest struct {
	StreamName         string `json:"StreamName"`
	StreamARN          string `json:"StreamARN"`
	ShardToSplit       string `json:"ShardToSplit"`
	NewStartingHashKey string `json:"NewStartingHashKey"`
}

func (h *Handler) splitShard(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *splitShardRequest) (any, error) {
		if err := h.kinesis.SplitShard(ctx, req.StreamName, req.StreamARN, req.ShardToSplit, req.NewStartingHashKey); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type encryptionRequest struct {
	StreamName     string `json:"StreamName"`
	StreamARN      string `json:"StreamARN"`
	EncryptionType string `json:"EncryptionType"`
	KeyID          string `json:"KeyId"`
}

func (h *Handler) startStreamEncryption(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *encryptionRequest) (any, error) {
		if err := h.kinesis.StartStreamEncryption(ctx, req.StreamName, req.StreamARN, req.EncryptionType, req.KeyID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) stopStreamEncryption(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *encryptionRequest) (any, error) {
		if err := h.kinesis.StopStreamEncryption(ctx, req.StreamName, req.StreamARN, req.EncryptionType, req.KeyID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type updateMaxRecordSizeRequest struct {
	StreamName         string `json:"StreamName"`
	StreamARN          string `json:"StreamARN"`
	MaxRecordSizeInKiB int32  `json:"MaxRecordSizeInKiB"`
}

func (h *Handler) updateMaxRecordSize(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateMaxRecordSizeRequest) (any, error) {
		if err := h.kinesis.UpdateMaxRecordSize(ctx, req.StreamName, req.StreamARN, req.MaxRecordSizeInKiB); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type updateWarmThroughputRequest struct {
	StreamName          string `json:"StreamName"`
	StreamARN           string `json:"StreamARN"`
	WarmThroughputMiBps int32  `json:"WarmThroughputMiBps"`
}

func (h *Handler) updateStreamWarmThroughput(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateWarmThroughputRequest) (any, error) {
		if err := h.kinesis.UpdateStreamWarmThroughput(ctx, req.StreamName, req.StreamARN, req.WarmThroughputMiBps); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

package kinesis

import (
	"context"
	"net/http"
)

// --- Enhanced fan-out consumers ---

type registerConsumerRequest struct {
	StreamARN    string `json:"StreamARN"`
	ConsumerName string `json:"ConsumerName"`
}

func (h *Handler) registerConsumer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *registerConsumerRequest) (any, error) {
		c, err := h.kinesis.RegisterStreamConsumer(ctx, req.StreamARN, req.ConsumerName)
		if err != nil {
			return nil, err
		}

		return map[string]any{"Consumer": consumerToWire(c)}, nil
	})
}

type consumerRefRequest struct {
	StreamARN    string `json:"StreamARN"`
	ConsumerName string `json:"ConsumerName"`
	ConsumerARN  string `json:"ConsumerARN"`
}

func (h *Handler) deregisterConsumer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *consumerRefRequest) (any, error) {
		if err := h.kinesis.DeregisterStreamConsumer(ctx, req.StreamARN, req.ConsumerName, req.ConsumerARN); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) describeConsumer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *consumerRefRequest) (any, error) {
		c, err := h.kinesis.DescribeStreamConsumer(ctx, req.StreamARN, req.ConsumerName, req.ConsumerARN)
		if err != nil {
			return nil, err
		}

		return map[string]any{"ConsumerDescription": consumerToWire(c)}, nil
	})
}

type listConsumersRequest struct {
	StreamARN  string `json:"StreamARN"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

func (h *Handler) listConsumers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listConsumersRequest) (any, error) {
		consumers, _, err := h.kinesis.ListStreamConsumers(ctx, req.StreamARN, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		out := make([]consumerJSON, 0, len(consumers))
		for i := range consumers {
			out = append(out, consumerToWire(&consumers[i]))
		}

		return map[string]any{"Consumers": out}, nil
	})
}

// --- Enhanced monitoring ---

type monitoringRequest struct {
	StreamName        string   `json:"StreamName"`
	StreamARN         string   `json:"StreamARN"`
	ShardLevelMetrics []string `json:"ShardLevelMetrics"`
}

func (h *Handler) enableMonitoring(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *monitoringRequest) (any, error) {
		before, after, err := h.kinesis.EnableEnhancedMonitoring(ctx, req.StreamName, req.StreamARN, req.ShardLevelMetrics)
		if err != nil {
			return nil, err
		}

		return monitoringResponse(req.StreamName, req.StreamARN, before, after), nil
	})
}

func (h *Handler) disableMonitoring(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *monitoringRequest) (any, error) {
		before, after, err := h.kinesis.DisableEnhancedMonitoring(ctx, req.StreamName, req.StreamARN, req.ShardLevelMetrics)
		if err != nil {
			return nil, err
		}

		return monitoringResponse(req.StreamName, req.StreamARN, before, after), nil
	})
}

func monitoringResponse(name, arn string, before, after []string) map[string]any {
	return map[string]any{
		"StreamName":               name,
		"CurrentShardLevelMetrics": before,
		"DesiredShardLevelMetrics": after,
		"StreamARN":                arn,
	}
}

// --- Tags ---

type addTagsRequest struct {
	StreamName string            `json:"StreamName"`
	StreamARN  string            `json:"StreamARN"`
	Tags       map[string]string `json:"Tags"`
}

func (h *Handler) addTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *addTagsRequest) (any, error) {
		if err := h.kinesis.AddTagsToStream(ctx, req.StreamName, req.StreamARN, req.Tags); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type removeTagsRequest struct {
	StreamName string   `json:"StreamName"`
	StreamARN  string   `json:"StreamARN"`
	TagKeys    []string `json:"TagKeys"`
}

func (h *Handler) removeTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *removeTagsRequest) (any, error) {
		if err := h.kinesis.RemoveTagsFromStream(ctx, req.StreamName, req.StreamARN, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsForStreamRequest struct {
	StreamName           string `json:"StreamName"`
	StreamARN            string `json:"StreamARN"`
	ExclusiveStartTagKey string `json:"ExclusiveStartTagKey"`
	Limit                int32  `json:"Limit"`
}

func (h *Handler) listTagsForStream(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsForStreamRequest) (any, error) {
		tags, more, err := h.kinesis.ListTagsForStream(ctx, req.StreamName, req.StreamARN, req.ExclusiveStartTagKey, req.Limit)
		if err != nil {
			return nil, err
		}

		return map[string]any{"Tags": tagsToWire(tags), "HasMoreTags": more}, nil
	})
}

type tagResourceRequest struct {
	ResourceARN string            `json:"ResourceARN"`
	Tags        map[string]string `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.kinesis.TagResource(ctx, req.ResourceARN, req.Tags); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceRequest struct {
	ResourceARN string   `json:"ResourceARN"`
	TagKeys     []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.kinesis.UntagResource(ctx, req.ResourceARN, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type resourceARNRequest struct {
	ResourceARN string `json:"ResourceARN"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceARNRequest) (any, error) {
		tags, err := h.kinesis.ListTagsForResource(ctx, req.ResourceARN)
		if err != nil {
			return nil, err
		}

		return map[string]any{"Tags": tagsToWire(tags)}, nil
	})
}

// --- Resource policy ---

type putPolicyRequest struct {
	ResourceARN string `json:"ResourceARN"`
	Policy      string `json:"Policy"`
}

func (h *Handler) putResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putPolicyRequest) (any, error) {
		if err := h.kinesis.PutResourcePolicy(ctx, req.ResourceARN, req.Policy); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) getResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceARNRequest) (any, error) {
		policy, err := h.kinesis.GetResourcePolicy(ctx, req.ResourceARN)
		if err != nil {
			return nil, err
		}

		return map[string]any{"Policy": policy}, nil
	})
}

func (h *Handler) deleteResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceARNRequest) (any, error) {
		if err := h.kinesis.DeleteResourcePolicy(ctx, req.ResourceARN); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// --- Account & limits ---

func (h *Handler) describeLimits(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, _ *struct{}) (any, error) {
		l, err := h.kinesis.DescribeLimits(ctx)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"ShardLimit":               l.ShardLimit,
			"OpenShardCount":           l.OpenShardCount,
			"OnDemandStreamCount":      l.OnDemandStreamCount,
			"OnDemandStreamCountLimit": l.OnDemandStreamCountLimit,
		}, nil
	})
}

type billingCommitmentJSON struct {
	Status string `json:"Status"`
}

func (h *Handler) describeAccountSettings(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, _ *struct{}) (any, error) {
		s, err := h.kinesis.DescribeAccountSettings(ctx)
		if err != nil {
			return nil, err
		}

		return accountSettingsResponse(s.CommitmentStatus), nil
	})
}

type updateAccountSettingsRequest struct {
	MinimumThroughputBillingCommitment *billingCommitmentJSON `json:"MinimumThroughputBillingCommitment"`
}

func (h *Handler) updateAccountSettings(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateAccountSettingsRequest) (any, error) {
		status := ""
		if req.MinimumThroughputBillingCommitment != nil {
			status = req.MinimumThroughputBillingCommitment.Status
		}

		s, err := h.kinesis.UpdateAccountSettings(ctx, kinesisAccountSettings(status))
		if err != nil {
			return nil, err
		}

		return accountSettingsResponse(s.CommitmentStatus), nil
	})
}

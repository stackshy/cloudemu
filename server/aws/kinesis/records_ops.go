package kinesis

import (
	"context"
	"net/http"
	"time"

	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

type putRecordRequest struct {
	StreamName                string `json:"StreamName"`
	StreamARN                 string `json:"StreamARN"`
	Data                      []byte `json:"Data"`
	PartitionKey              string `json:"PartitionKey"`
	ExplicitHashKey           string `json:"ExplicitHashKey"`
	SequenceNumberForOrdering string `json:"SequenceNumberForOrdering"`
}

func (h *Handler) putRecord(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRecordRequest) (any, error) {
		res, err := h.kinesis.PutRecord(ctx, kinesisdriver.PutRecordInput{
			StreamName:                req.StreamName,
			StreamARN:                 req.StreamARN,
			Data:                      req.Data,
			PartitionKey:              req.PartitionKey,
			ExplicitHashKey:           req.ExplicitHashKey,
			SequenceNumberForOrdering: req.SequenceNumberForOrdering,
		})
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"ShardId":        res.ShardID,
			"SequenceNumber": res.SequenceNumber,
			"EncryptionType": res.EncryptionType,
		}, nil
	})
}

type putRecordsEntryJSON struct {
	Data            []byte `json:"Data"`
	PartitionKey    string `json:"PartitionKey"`
	ExplicitHashKey string `json:"ExplicitHashKey"`
}

type putRecordsRequest struct {
	StreamName string                `json:"StreamName"`
	StreamARN  string                `json:"StreamARN"`
	Records    []putRecordsEntryJSON `json:"Records"`
}

type putRecordsResultJSON struct {
	SequenceNumber string `json:"SequenceNumber,omitempty"`
	ShardID        string `json:"ShardId,omitempty"`
	ErrorCode      string `json:"ErrorCode,omitempty"`
	ErrorMessage   string `json:"ErrorMessage,omitempty"`
}

func (h *Handler) putRecords(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRecordsRequest) (any, error) {
		entries := make([]kinesisdriver.PutRecordsRequestEntry, 0, len(req.Records))
		for i := range req.Records {
			entries = append(entries, kinesisdriver.PutRecordsRequestEntry{
				Data:            req.Records[i].Data,
				PartitionKey:    req.Records[i].PartitionKey,
				ExplicitHashKey: req.Records[i].ExplicitHashKey,
			})
		}

		results, failed, err := h.kinesis.PutRecords(ctx, req.StreamName, req.StreamARN, entries)
		if err != nil {
			return nil, err
		}

		wireResults := make([]putRecordsResultJSON, 0, len(results))
		for i := range results {
			wireResults = append(wireResults, putRecordsResultJSON{
				SequenceNumber: results[i].SequenceNumber,
				ShardID:        results[i].ShardID,
				ErrorCode:      results[i].ErrorCode,
				ErrorMessage:   results[i].ErrorMessage,
			})
		}

		return map[string]any{
			"FailedRecordCount": failed,
			"Records":           wireResults,
		}, nil
	})
}

type getShardIteratorRequest struct {
	StreamName             string   `json:"StreamName"`
	StreamARN              string   `json:"StreamARN"`
	ShardID                string   `json:"ShardId"`
	ShardIteratorType      string   `json:"ShardIteratorType"`
	StartingSequenceNumber string   `json:"StartingSequenceNumber"`
	Timestamp              *float64 `json:"Timestamp"`
}

func (h *Handler) getShardIterator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getShardIteratorRequest) (any, error) {
		var ts time.Time
		if req.Timestamp != nil {
			ts = time.Unix(int64(*req.Timestamp), 0).UTC()
		}

		it, err := h.kinesis.GetShardIterator(ctx, kinesisdriver.GetShardIteratorInput{
			StreamName:             req.StreamName,
			StreamARN:              req.StreamARN,
			ShardID:                req.ShardID,
			ShardIteratorType:      req.ShardIteratorType,
			StartingSequenceNumber: req.StartingSequenceNumber,
			Timestamp:              ts,
		})
		if err != nil {
			return nil, err
		}

		return map[string]any{"ShardIterator": it}, nil
	})
}

type getRecordsRequest struct {
	ShardIterator string `json:"ShardIterator"`
	Limit         int32  `json:"Limit"`
	StreamARN     string `json:"StreamARN"`
}

func (h *Handler) getRecords(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getRecordsRequest) (any, error) {
		out, err := h.kinesis.GetRecords(ctx, req.ShardIterator, req.Limit)
		if err != nil {
			return nil, err
		}

		resp := map[string]any{
			"Records":            recordsToWire(out.Records),
			"MillisBehindLatest": out.MillisBehindLatest,
		}
		if out.NextShardIterator != "" {
			resp["NextShardIterator"] = out.NextShardIterator
		}

		if cs := childShardsToWire(out.ChildShards); cs != nil {
			resp["ChildShards"] = cs
		}

		return resp, nil
	})
}

type listShardsRequest struct {
	StreamName            string `json:"StreamName"`
	StreamARN             string `json:"StreamARN"`
	NextToken             string `json:"NextToken"`
	MaxResults            int32  `json:"MaxResults"`
	ExclusiveStartShardID string `json:"ExclusiveStartShardId"`
}

func (h *Handler) listShards(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listShardsRequest) (any, error) {
		out, err := h.kinesis.ListShards(ctx, kinesisdriver.ListShardsInput{
			StreamName:            req.StreamName,
			StreamARN:             req.StreamARN,
			NextToken:             req.NextToken,
			MaxResults:            req.MaxResults,
			ExclusiveStartShardID: req.ExclusiveStartShardID,
		})
		if err != nil {
			return nil, err
		}

		resp := map[string]any{"Shards": shardsToWire(out.Shards)}
		if out.NextToken != "" {
			resp["NextToken"] = out.NextToken
		}

		return resp, nil
	})
}

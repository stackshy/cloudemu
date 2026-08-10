package cloudtrail

import (
	"context"
	"net/http"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// --- request/response shapes ---

type createTrailRequest struct {
	Name                       string `json:"Name"`
	S3BucketName               string `json:"S3BucketName"`
	S3KeyPrefix                string `json:"S3KeyPrefix"`
	SnsTopicName               string `json:"SnsTopicName"`
	IncludeGlobalServiceEvents *bool  `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         bool   `json:"IsMultiRegionTrail"`
	IsOrganizationTrail        bool   `json:"IsOrganizationTrail"`
	EnableLogFileValidation    bool   `json:"EnableLogFileValidation"`
	CloudWatchLogsLogGroupArn  string `json:"CloudWatchLogsLogGroupArn"`
	CloudWatchLogsRoleArn      string `json:"CloudWatchLogsRoleArn"`
	KmsKeyID                   string `json:"KmsKeyId"`
	TagsList                   []tag  `json:"TagsList"`
}

type updateTrailRequest struct {
	Name                       string  `json:"Name"`
	S3BucketName               *string `json:"S3BucketName"`
	S3KeyPrefix                *string `json:"S3KeyPrefix"`
	SnsTopicName               *string `json:"SnsTopicName"`
	IncludeGlobalServiceEvents *bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         *bool   `json:"IsMultiRegionTrail"`
	IsOrganizationTrail        *bool   `json:"IsOrganizationTrail"`
	EnableLogFileValidation    *bool   `json:"EnableLogFileValidation"`
	CloudWatchLogsLogGroupArn  *string `json:"CloudWatchLogsLogGroupArn"`
	CloudWatchLogsRoleArn      *string `json:"CloudWatchLogsRoleArn"`
	KmsKeyID                   *string `json:"KmsKeyId"`
}

type nameRequest struct {
	Name string `json:"Name"`
}

type describeTrailsRequest struct {
	TrailNameList []string `json:"trailNameList"`
}

type listTrailsRequest struct {
	NextToken string `json:"NextToken"`
}

func (h *Handler) createTrail(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createTrailRequest) (any, error) {
		t, err := h.ct.CreateTrail(ctx, ctdriver.CreateTrailInput{
			Name:                       req.Name,
			S3BucketName:               req.S3BucketName,
			S3KeyPrefix:                req.S3KeyPrefix,
			SNSTopicName:               req.SnsTopicName,
			IncludeGlobalServiceEvents: req.IncludeGlobalServiceEvents,
			IsMultiRegionTrail:         req.IsMultiRegionTrail,
			IsOrganizationTrail:        req.IsOrganizationTrail,
			LogFileValidationEnabled:   req.EnableLogFileValidation,
			CloudWatchLogsLogGroupARN:  req.CloudWatchLogsLogGroupArn,
			CloudWatchLogsRoleARN:      req.CloudWatchLogsRoleArn,
			KMSKeyID:                   req.KmsKeyID,
			Tags:                       tagsToMap(req.TagsList),
		})
		if err != nil {
			return nil, err
		}

		return trailToWire(t), nil
	})
}

func (h *Handler) getTrail(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameRequest) (any, error) {
		t, err := h.ct.GetTrail(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return struct {
			Trail trailJSON `json:"Trail"`
		}{Trail: trailToWire(t)}, nil
	})
}

func (h *Handler) updateTrail(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateTrailRequest) (any, error) {
		t, err := h.ct.UpdateTrail(ctx, ctdriver.UpdateTrailInput{
			Name:                       req.Name,
			S3BucketName:               req.S3BucketName,
			S3KeyPrefix:                req.S3KeyPrefix,
			SNSTopicName:               req.SnsTopicName,
			IncludeGlobalServiceEvents: req.IncludeGlobalServiceEvents,
			IsMultiRegionTrail:         req.IsMultiRegionTrail,
			IsOrganizationTrail:        req.IsOrganizationTrail,
			LogFileValidationEnabled:   req.EnableLogFileValidation,
			CloudWatchLogsLogGroupARN:  req.CloudWatchLogsLogGroupArn,
			CloudWatchLogsRoleARN:      req.CloudWatchLogsRoleArn,
			KMSKeyID:                   req.KmsKeyID,
		})
		if err != nil {
			return nil, err
		}

		return trailToWire(t), nil
	})
}

func (h *Handler) deleteTrail(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameRequest) (any, error) {
		if err := h.ct.DeleteTrail(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) describeTrails(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeTrailsRequest) (any, error) {
		trails, err := h.ct.DescribeTrails(ctx, req.TrailNameList)
		if err != nil {
			return nil, err
		}

		list := make([]trailJSON, 0, len(trails))
		for i := range trails {
			list = append(list, trailToWire(&trails[i]))
		}

		return struct {
			TrailList []trailJSON `json:"trailList"`
		}{TrailList: list}, nil
	})
}

func (h *Handler) listTrails(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTrailsRequest) (any, error) {
		trails, next, err := h.ct.ListTrails(ctx, req.NextToken)
		if err != nil {
			return nil, err
		}

		type trailInfoJSON struct {
			Name       string `json:"Name,omitempty"`
			TrailARN   string `json:"TrailARN,omitempty"`
			HomeRegion string `json:"HomeRegion,omitempty"`
		}

		list := make([]trailInfoJSON, 0, len(trails))
		for i := range trails {
			list = append(list, trailInfoJSON{
				Name: trails[i].Name, TrailARN: trails[i].TrailARN, HomeRegion: trails[i].HomeRegion,
			})
		}

		return struct {
			Trails    []trailInfoJSON `json:"Trails"`
			NextToken string          `json:"NextToken,omitempty"`
		}{Trails: list, NextToken: next}, nil
	})
}

func (h *Handler) getTrailStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameRequest) (any, error) {
		st, err := h.ct.GetTrailStatus(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return struct {
			IsLogging          bool     `json:"IsLogging"`
			StartLoggingTime   *float64 `json:"StartLoggingTime,omitempty"`
			StopLoggingTime    *float64 `json:"StopLoggingTime,omitempty"`
			LatestDeliveryTime *float64 `json:"LatestDeliveryTime,omitempty"`
		}{
			IsLogging:          st.IsLogging,
			StartLoggingTime:   epochOrNil(st.StartLoggingTime),
			StopLoggingTime:    epochOrNil(st.StopLoggingTime),
			LatestDeliveryTime: epochOrNil(st.LatestDeliveryTime),
		}, nil
	})
}

func (h *Handler) startLogging(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameRequest) (any, error) {
		if err := h.ct.StartLogging(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) stopLogging(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameRequest) (any, error) {
		if err := h.ct.StopLogging(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

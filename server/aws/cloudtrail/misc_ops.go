package cloudtrail

import (
	"context"
	"net/http"
	"time"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

func (h *Handler) putResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ResourceArn    string `json:"ResourceArn"`
		ResourcePolicy string `json:"ResourcePolicy"`
	},
	) (any, error) {
		arn, policy, err := h.ct.PutResourcePolicy(ctx, req.ResourceArn, req.ResourcePolicy)
		if err != nil {
			return nil, err
		}

		return struct {
			ResourceArn    string `json:"ResourceArn"`
			ResourcePolicy string `json:"ResourcePolicy"`
		}{ResourceArn: arn, ResourcePolicy: policy}, nil
	})
}

func (h *Handler) getResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ResourceArn string `json:"ResourceArn"`
	},
	) (any, error) {
		arn, policy, err := h.ct.GetResourcePolicy(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return struct {
			ResourceArn    string `json:"ResourceArn"`
			ResourcePolicy string `json:"ResourcePolicy"`
		}{ResourceArn: arn, ResourcePolicy: policy}, nil
	})
}

func (h *Handler) deleteResourcePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ResourceArn string `json:"ResourceArn"`
	},
	) (any, error) {
		if err := h.ct.DeleteResourcePolicy(ctx, req.ResourceArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) putEventConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore string `json:"EventDataStore"`
		MaxEventSize   string `json:"MaxEventSize"`
	},
	) (any, error) {
		arn, size, err := h.ct.PutEventConfiguration(ctx, req.EventDataStore, req.MaxEventSize)
		if err != nil {
			return nil, err
		}

		return struct {
			EventDataStoreArn string `json:"EventDataStoreArn,omitempty"`
			MaxEventSize      string `json:"MaxEventSize,omitempty"`
		}{EventDataStoreArn: arn, MaxEventSize: size}, nil
	})
}

func (h *Handler) getEventConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore string `json:"EventDataStore"`
	},
	) (any, error) {
		arn, size, err := h.ct.GetEventConfiguration(ctx, req.EventDataStore)
		if err != nil {
			return nil, err
		}

		return struct {
			EventDataStoreArn string `json:"EventDataStoreArn,omitempty"`
			MaxEventSize      string `json:"MaxEventSize,omitempty"`
		}{EventDataStoreArn: arn, MaxEventSize: size}, nil
	})
}

func (h *Handler) enableFederation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore    string `json:"EventDataStore"`
		FederationRoleArn string `json:"FederationRoleArn"`
	},
	) (any, error) {
		arn, role, status, err := h.ct.EnableFederation(ctx, req.EventDataStore, req.FederationRoleArn)
		if err != nil {
			return nil, err
		}

		return struct {
			EventDataStoreArn string `json:"EventDataStoreArn,omitempty"`
			FederationRoleArn string `json:"FederationRoleArn,omitempty"`
			FederationStatus  string `json:"FederationStatus,omitempty"`
		}{EventDataStoreArn: arn, FederationRoleArn: role, FederationStatus: status}, nil
	})
}

func (h *Handler) disableFederation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore string `json:"EventDataStore"`
	},
	) (any, error) {
		arn, status, err := h.ct.DisableFederation(ctx, req.EventDataStore)
		if err != nil {
			return nil, err
		}

		return struct {
			EventDataStoreArn string `json:"EventDataStoreArn,omitempty"`
			FederationStatus  string `json:"FederationStatus,omitempty"`
		}{EventDataStoreArn: arn, FederationStatus: status}, nil
	})
}

func (h *Handler) addTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ResourceID string `json:"ResourceId"`
		TagsList   []tag  `json:"TagsList"`
	},
	) (any, error) {
		if err := h.ct.AddTags(ctx, req.ResourceID, tagsToMap(req.TagsList)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) removeTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ResourceID string `json:"ResourceId"`
		TagsList   []tag  `json:"TagsList"`
	},
	) (any, error) {
		keys := make([]string, 0, len(req.TagsList))
		for _, t := range req.TagsList {
			keys = append(keys, t.Key)
		}

		if err := h.ct.RemoveTags(ctx, req.ResourceID, keys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ResourceIDList []string `json:"ResourceIdList"`
		NextToken      string   `json:"NextToken"`
	},
	) (any, error) {
		tagsByRes, err := h.ct.ListTags(ctx, req.ResourceIDList)
		if err != nil {
			return nil, err
		}

		type resourceTag struct {
			ResourceID string `json:"ResourceId"`
			TagsList   []tag  `json:"TagsList"`
		}

		list := make([]resourceTag, 0, len(req.ResourceIDList))
		for _, id := range req.ResourceIDList {
			list = append(list, resourceTag{ResourceID: id, TagsList: mapToTags(tagsByRes[id])})
		}

		return struct {
			ResourceTagList []resourceTag `json:"ResourceTagList"`
		}{ResourceTagList: list}, nil
	})
}

func (h *Handler) registerOrgAdmin(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		MemberAccountID string `json:"MemberAccountId"`
	},
	) (any, error) {
		if err := h.ct.RegisterOrganizationDelegatedAdmin(ctx, req.MemberAccountID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deregisterOrgAdmin(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		DelegatedAdminAccountID string `json:"DelegatedAdminAccountId"`
	},
	) (any, error) {
		if err := h.ct.DeregisterOrganizationDelegatedAdmin(ctx, req.DelegatedAdminAccountID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) lookupEvents(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		LookupAttributes []struct {
			AttributeKey   string `json:"AttributeKey"`
			AttributeValue string `json:"AttributeValue"`
		} `json:"LookupAttributes"`
		StartTime     *float64 `json:"StartTime"`
		EndTime       *float64 `json:"EndTime"`
		EventCategory string   `json:"EventCategory"`
		NextToken     string   `json:"NextToken"`
		MaxResults    int32    `json:"MaxResults"`
	},
	) (any, error) {
		in := ctdriver.LookupInput{
			NextToken:     req.NextToken,
			MaxResults:    req.MaxResults,
			EventCategory: req.EventCategory,
		}

		if req.StartTime != nil {
			in.StartTime = time.Unix(int64(*req.StartTime), 0).UTC()
		}

		if req.EndTime != nil {
			in.EndTime = time.Unix(int64(*req.EndTime), 0).UTC()
		}

		for _, a := range req.LookupAttributes {
			in.LookupAttributes = append(in.LookupAttributes,
				ctdriver.LookupAttribute{AttributeKey: a.AttributeKey, AttributeValue: a.AttributeValue})
		}

		events, next, err := h.ct.LookupEvents(ctx, in)
		if err != nil {
			return nil, err
		}

		list := make([]lookupEventJSON, 0, len(events))
		for i := range events {
			list = append(list, toLookupEventJSON(&events[i]))
		}

		return struct {
			Events    []lookupEventJSON `json:"Events"`
			NextToken string            `json:"NextToken,omitempty"`
		}{Events: list, NextToken: next}, nil
	})
}

// lookupEventJSON is the LookupEvents result-event wire shape.
type lookupEventJSON struct {
	EventID         string   `json:"EventId,omitempty"`
	EventName       string   `json:"EventName,omitempty"`
	ReadOnly        string   `json:"ReadOnly,omitempty"`
	AccessKeyID     string   `json:"AccessKeyId,omitempty"`
	EventTime       *float64 `json:"EventTime,omitempty"`
	EventSource     string   `json:"EventSource,omitempty"`
	Username        string   `json:"Username,omitempty"`
	CloudTrailEvent string   `json:"CloudTrailEvent,omitempty"`
}

func toLookupEventJSON(e *ctdriver.Event) lookupEventJSON {
	out := lookupEventJSON{
		EventID:         e.EventID,
		EventName:       e.EventName,
		ReadOnly:        e.ReadOnly,
		AccessKeyID:     e.AccessKeyID,
		EventSource:     e.EventSource,
		Username:        e.Username,
		CloudTrailEvent: e.CloudTrailEvent,
	}

	if !e.EventTime.IsZero() {
		secs := float64(e.EventTime.Unix())
		out.EventTime = &secs
	}

	return out
}

func (h *Handler) listPublicKeys(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		StartTime *float64 `json:"StartTime"`
		EndTime   *float64 `json:"EndTime"`
		NextToken string   `json:"NextToken"`
	},
	) (any, error) {
		var start, end time.Time
		if req.StartTime != nil {
			start = time.Unix(int64(*req.StartTime), 0).UTC()
		}

		if req.EndTime != nil {
			end = time.Unix(int64(*req.EndTime), 0).UTC()
		}

		keys, next, err := h.ct.ListPublicKeys(ctx, start, end, req.NextToken)
		if err != nil {
			return nil, err
		}

		type keyJSON struct {
			Fingerprint string `json:"Fingerprint,omitempty"`
		}

		list := make([]keyJSON, 0, len(keys))
		for i := range keys {
			list = append(list, keyJSON{Fingerprint: keys[i].Fingerprint})
		}

		return struct {
			PublicKeyList []keyJSON `json:"PublicKeyList"`
			NextToken     string    `json:"NextToken,omitempty"`
		}{PublicKeyList: list, NextToken: next}, nil
	})
}

func (h *Handler) listInsightsData(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		NextToken string `json:"NextToken"`
	},
	) (any, error) {
		if _, next, err := h.ct.ListInsightsData(ctx, req.NextToken); err != nil {
			_ = next

			return nil, err
		}

		return struct {
			InsightsData any    `json:"InsightsData,omitempty"`
			NextToken    string `json:"NextToken,omitempty"`
		}{}, nil
	})
}

func (h *Handler) listInsightsMetricData(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		NextToken string `json:"NextToken"`
	},
	) (any, error) {
		if _, _, err := h.ct.ListInsightsMetricData(ctx, req.NextToken); err != nil {
			return nil, err
		}

		return struct {
			Timestamps []float64 `json:"Timestamps,omitempty"`
			Values     []float64 `json:"Values,omitempty"`
			NextToken  string    `json:"NextToken,omitempty"`
		}{}, nil
	})
}

//nolint:dupl // sample-query search shape mirrors listImportFailures but returns a distinct wire type.
func (h *Handler) searchSampleQueries(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		SearchPhrase string `json:"SearchPhrase"`
		NextToken    string `json:"NextToken"`
		MaxResults   int32  `json:"MaxResults"`
	},
	) (any, error) {
		samples, next, err := h.ct.SearchSampleQueries(ctx, req.SearchPhrase, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		type sampleJSON struct {
			Name        string  `json:"Name,omitempty"`
			Description string  `json:"Description,omitempty"`
			SQL         string  `json:"SQL,omitempty"`
			Relevance   float32 `json:"Relevance,omitempty"`
		}

		list := make([]sampleJSON, 0, len(samples))
		for i := range samples {
			list = append(list, sampleJSON{
				Name: samples[i].Name, Description: samples[i].Description,
				SQL: samples[i].SQL, Relevance: samples[i].Relevance,
			})
		}

		return struct {
			SearchResults []sampleJSON `json:"SearchResults"`
			NextToken     string       `json:"NextToken,omitempty"`
		}{SearchResults: list, NextToken: next}, nil
	})
}

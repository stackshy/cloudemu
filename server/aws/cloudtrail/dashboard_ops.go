package cloudtrail

import (
	"context"
	"net/http"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

type refreshScheduleJSON struct {
	Frequency *struct {
		Unit  string `json:"Unit"`
		Value int32  `json:"Value"`
	} `json:"Frequency"`
	Status string `json:"Status,omitempty"`
}

type createDashboardRequest struct {
	Name                         string               `json:"Name"`
	RefreshSchedule              *refreshScheduleJSON `json:"RefreshSchedule"`
	TerminationProtectionEnabled bool                 `json:"TerminationProtectionEnabled"`
	TagsList                     []tag                `json:"TagsList"`
}

func dashboardFromRefresh(name string, rs *refreshScheduleJSON, tp bool, tags []tag) ctdriver.Dashboard {
	d := ctdriver.Dashboard{Name: name, TerminationProtectionEnabled: tp, Tags: tagsToMap(tags)}
	if rs != nil {
		d.RefreshScheduleStatus = rs.Status
		if rs.Frequency != nil {
			d.RefreshScheduleFrequencyUnit = rs.Frequency.Unit
			d.RefreshScheduleFrequencyVal = rs.Frequency.Value
		}
	}

	return d
}

func (h *Handler) createDashboard(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createDashboardRequest) (any, error) {
		d, err := h.ct.CreateDashboard(ctx,
			dashboardFromRefresh(req.Name, req.RefreshSchedule, req.TerminationProtectionEnabled, req.TagsList))
		if err != nil {
			return nil, err
		}

		return struct {
			DashboardArn string `json:"DashboardArn"`
			Name         string `json:"Name"`
			Type         string `json:"Type,omitempty"`
		}{DashboardArn: d.ARN, Name: d.Name, Type: d.Type}, nil
	})
}

func (h *Handler) getDashboard(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		DashboardID string `json:"DashboardId"`
	},
	) (any, error) {
		d, err := h.ct.GetDashboard(ctx, req.DashboardID)
		if err != nil {
			return nil, err
		}

		return struct {
			DashboardArn                 string   `json:"DashboardArn,omitempty"`
			Type                         string   `json:"Type,omitempty"`
			Status                       string   `json:"Status,omitempty"`
			TerminationProtectionEnabled bool     `json:"TerminationProtectionEnabled"`
			CreatedTimestamp             *float64 `json:"CreatedTimestamp,omitempty"`
			UpdatedTimestamp             *float64 `json:"UpdatedTimestamp,omitempty"`
		}{
			DashboardArn: d.ARN, Type: d.Type, Status: d.Status,
			TerminationProtectionEnabled: d.TerminationProtectionEnabled,
			CreatedTimestamp:             epochOrNil(d.CreatedAt), UpdatedTimestamp: epochOrNil(d.UpdatedAt),
		}, nil
	})
}

func (h *Handler) updateDashboard(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		DashboardID                  string               `json:"DashboardId"`
		RefreshSchedule              *refreshScheduleJSON `json:"RefreshSchedule"`
		TerminationProtectionEnabled bool                 `json:"TerminationProtectionEnabled"`
	},
	) (any, error) {
		d, err := h.ct.UpdateDashboard(ctx,
			dashboardFromRefresh(req.DashboardID, req.RefreshSchedule, req.TerminationProtectionEnabled, nil))
		if err != nil {
			return nil, err
		}

		return struct {
			DashboardArn string `json:"DashboardArn"`
			Name         string `json:"Name"`
		}{DashboardArn: d.ARN, Name: d.Name}, nil
	})
}

func (h *Handler) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		DashboardID string `json:"DashboardId"`
	},
	) (any, error) {
		if err := h.ct.DeleteDashboard(ctx, req.DashboardID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

//nolint:dupl // list-op shape mirrors listEventDataStores but returns a distinct wire type.
func (h *Handler) listDashboards(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listRequest) (any, error) {
		dashboards, next, err := h.ct.ListDashboards(ctx, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		list := make([]dashboardJSON, 0, len(dashboards))
		for i := range dashboards {
			list = append(list, dashboardToWire(&dashboards[i]))
		}

		return struct {
			Dashboards []dashboardJSON `json:"Dashboards"`
			NextToken  string          `json:"NextToken,omitempty"`
		}{Dashboards: list, NextToken: next}, nil
	})
}

func (h *Handler) startDashboardRefresh(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		DashboardID string `json:"DashboardId"`
	},
	) (any, error) {
		id, err := h.ct.StartDashboardRefresh(ctx, req.DashboardID)
		if err != nil {
			return nil, err
		}

		return struct {
			RefreshID string `json:"RefreshId"`
		}{RefreshID: id}, nil
	})
}

package sql

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	subSTR = "backupShortTermRetentionPolicies"
	subLTR = "backupLongTermRetentionPolicies"
)

// retentionProps is the union of the short- and long-term policy properties.
type retentionProps struct {
	RetentionDays             int    `json:"retentionDays,omitempty"`
	DiffBackupIntervalInHours int    `json:"diffBackupIntervalInHours,omitempty"`
	WeeklyRetention           string `json:"weeklyRetention,omitempty"`
	MonthlyRetention          string `json:"monthlyRetention,omitempty"`
	YearlyRetention           string `json:"yearlyRetention,omitempty"`
	WeekOfYear                int    `json:"weekOfYear,omitempty"`
	ConnectionType            string `json:"connectionType,omitempty"`
}

type policyBody struct {
	Properties retentionProps `json:"properties"`
}

// serveSingletonPolicy routes a modeled singleton collection or its "default"
// item: GET lists or reads, PUT and PATCH write through write, and DELETE is
// not part of the API. get and write return the current properties.
func serveSingletonPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, name string,
	get func() (retentionProps, error), write policyWriter,
) {
	switch {
	case name != "" && !strings.EqualFold(name, singletonDefault):
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "policy "+name+" not found")
		return
	case name == "" && r.Method != http.MethodGet:
		writeMethodNotAllowed(w)
		return
	}

	cur, err := get()
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if r.Method != http.MethodGet && !writePolicy(w, r, &cur, write) {
		return
	}

	id := strings.TrimSuffix(r.URL.Path, "/")
	if name == "" {
		item := singletonEnvelope(id+"/"+singletonDefault, singletonDefault, rp, cur)
		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": []any{item}})

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, singletonEnvelope(id, singletonDefault, rp, cur))
}

// writePolicy applies a PUT or PATCH body through write; it reports false
// once it has written an error response.
func writePolicy(w http.ResponseWriter, r *http.Request, cur *retentionProps, write policyWriter) bool {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		writeMethodNotAllowed(w)
		return false
	}

	var body policyBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return false
	}

	out, err := write(cur, &body.Properties, r.Method == http.MethodPatch)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return false
	}

	*cur = out

	return true
}

// policyWriter stores in (merged onto cur for a PATCH) and returns the result.
type policyWriter func(cur, in *retentionProps, merge bool) (retentionProps, error)

// serveRetention answers databases/{d}/backup{Short,Long}TermRetentionPolicies.
func (h *Handler) serveRetention(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ret, ok := h.db.(rdsdriver.DatabaseRetentionPolicies)
	if !ok {
		writeUnsupported(w, rp.SubResourceAction)
		return
	}

	ctx, server, db := r.Context(), rp.ResourceName, rp.SubResourceName

	if strings.EqualFold(rp.SubResourceAction, subSTR) {
		serveSingletonPolicy(w, r, rp, rp.Rest, func() (retentionProps, error) {
			p, err := ret.GetShortTermRetention(ctx, server, db)
			if err != nil {
				return retentionProps{}, err
			}

			return retentionProps{RetentionDays: p.RetentionDays, DiffBackupIntervalInHours: p.DiffBackupIntervalInHours}, nil
		}, func(cur, in *retentionProps, merge bool) (retentionProps, error) {
			if merge {
				mergeInts(cur, in)
			}

			p, err := ret.SetShortTermRetention(ctx, &rdsdriver.ShortTermRetentionPolicy{Server: server, Database: db,
				RetentionDays: in.RetentionDays, DiffBackupIntervalInHours: in.DiffBackupIntervalInHours})
			if err != nil {
				return retentionProps{}, err
			}

			return retentionProps{RetentionDays: p.RetentionDays, DiffBackupIntervalInHours: p.DiffBackupIntervalInHours}, nil
		})

		return
	}

	serveSingletonPolicy(w, r, rp, rp.Rest, func() (retentionProps, error) {
		p, err := ret.GetLongTermRetention(ctx, server, db)
		if err != nil {
			return retentionProps{}, err
		}

		return ltrProps(p), nil
	}, func(cur, in *retentionProps, merge bool) (retentionProps, error) {
		if merge {
			mergeLTR(cur, in)
		}

		p, err := ret.SetLongTermRetention(ctx, &rdsdriver.LongTermRetentionPolicy{Server: server, Database: db,
			WeeklyRetention: in.WeeklyRetention, MonthlyRetention: in.MonthlyRetention,
			YearlyRetention: in.YearlyRetention, WeekOfYear: in.WeekOfYear})
		if err != nil {
			return retentionProps{}, err
		}

		return ltrProps(p), nil
	})
}

// serveConnectionPolicy answers servers/{s}/connectionPolicies[/default].
func (h *Handler) serveConnectionPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cp, ok := h.db.(rdsdriver.ServerConnectionPolicies)
	if !ok {
		writeUnsupported(w, subConnectionPolicies)
		return
	}

	ctx, server := r.Context(), rp.ResourceName

	serveSingletonPolicy(w, r, rp, rp.SubResourceName, func() (retentionProps, error) {
		v, err := cp.GetConnectionPolicy(ctx, server)
		return retentionProps{ConnectionType: v}, err
	}, func(cur, in *retentionProps, merge bool) (retentionProps, error) {
		if merge && in.ConnectionType == "" {
			return *cur, nil
		}

		v, err := cp.SetConnectionPolicy(ctx, server, in.ConnectionType)

		return retentionProps{ConnectionType: v}, err
	})
}

func ltrProps(p *rdsdriver.LongTermRetentionPolicy) retentionProps {
	return retentionProps{WeeklyRetention: p.WeeklyRetention, MonthlyRetention: p.MonthlyRetention,
		YearlyRetention: p.YearlyRetention, WeekOfYear: p.WeekOfYear}
}

// mergeInts fills the PATCH request's unset short-term fields from cur.
func mergeInts(cur, in *retentionProps) {
	if in.RetentionDays == 0 {
		in.RetentionDays = cur.RetentionDays
	}

	if in.DiffBackupIntervalInHours == 0 {
		in.DiffBackupIntervalInHours = cur.DiffBackupIntervalInHours
	}
}

// mergeLTR fills the PATCH request's unset long-term fields from cur.
func mergeLTR(cur, in *retentionProps) {
	for _, f := range []struct{ dst, src *string }{
		{&in.WeeklyRetention, &cur.WeeklyRetention},
		{&in.MonthlyRetention, &cur.MonthlyRetention},
		{&in.YearlyRetention, &cur.YearlyRetention},
	} {
		if *f.dst == "" {
			*f.dst = *f.src
		}
	}

	if in.WeekOfYear == 0 {
		in.WeekOfYear = cur.WeekOfYear
	}
}

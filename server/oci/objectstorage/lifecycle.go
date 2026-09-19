package objectstorage

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Lifecycle rule actions OCI accepts.
const (
	lifecycleDelete   = "DELETE"
	lifecycleArchive  = "ARCHIVE"
	lifecycleInfreq   = "INFREQUENT_ACCESS"
	lifecycleAbortMPU = "ABORT"
)

// Lifecycle time units.
const (
	unitDays  = "DAYS"
	unitYears = "YEARS"
)

const daysPerYear = 365

// serveLifecycle routes the object lifecycle policy at /l.
func (h *Handler) serveLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		h.putLifecycle(w, r, bucket)
	case http.MethodGet:
		h.getLifecycle(w, r, bucket)
	case http.MethodDelete:
		h.deleteLifecycle(w, r, bucket)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) putLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	var req lifecycleBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	cfg, err := toLifecycleConfig(req)
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, err.Error())
		return
	}

	if err := h.store.PutLifecycleConfig(r.Context(), bucket, cfg); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toLifecycleBody(cfg))
}

func (h *Handler) getLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := h.store.GetLifecycleConfig(r.Context(), bucket)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toLifecycleBody(*cfg))
}

func (h *Handler) deleteLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := h.extras.DeleteLifecyclePolicy(r.Context(), bucket); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// toLifecycleConfig maps OCI's rules onto the portable shape, rejecting what
// the portable rule cannot hold rather than dropping it.
func toLifecycleConfig(body lifecycleBody) (driver.LifecycleConfig, error) {
	cfg := driver.LifecycleConfig{Rules: make([]driver.LifecycleRule, 0, len(body.Items))}

	for _, item := range body.Items {
		rule, err := toLifecycleRule(item)
		if err != nil {
			return driver.LifecycleConfig{}, err
		}

		cfg.Rules = append(cfg.Rules, rule)
	}

	return cfg, nil
}

func toLifecycleRule(item lifecycleRuleBody) (driver.LifecycleRule, error) {
	days, err := lifecycleDays(item.TimeAmount, item.TimeUnit)
	if err != nil {
		return driver.LifecycleRule{}, err
	}

	prefix, err := inclusionPrefix(item)
	if err != nil {
		return driver.LifecycleRule{}, err
	}

	rule := driver.LifecycleRule{ID: item.Name, Enabled: item.IsEnabled, Prefix: prefix}

	switch strings.ToUpper(item.Action) {
	case lifecycleDelete:
		rule.ExpirationDays = days
	case lifecycleArchive:
		rule.TransitionDays, rule.TransitionStorageClass = days, lifecycleArchive
	case lifecycleInfreq:
		rule.TransitionDays, rule.TransitionStorageClass = days, lifecycleInfreq
	case lifecycleAbortMPU:
		rule.AbortMultipartDays = days
	default:
		return driver.LifecycleRule{}, &lifecycleError{"unsupported lifecycle action " + item.Action}
	}

	return rule, nil
}

// inclusionPrefix reduces the filter to the single prefix the portable rule
// holds, refusing a filter that would lose prefixes.
func inclusionPrefix(item lifecycleRuleBody) (string, error) {
	if item.ObjectNameFilter == nil || len(item.ObjectNameFilter.InclusionPrefixes) == 0 {
		return "", nil
	}

	if len(item.ObjectNameFilter.InclusionPrefixes) > 1 {
		return "", &lifecycleError{"rule " + item.Name + " names more than one inclusionPrefix, which is not emulated"}
	}

	return item.ObjectNameFilter.InclusionPrefixes[0], nil
}

func lifecycleDays(amount int64, unit string) (int, error) {
	switch strings.ToUpper(unit) {
	case unitDays, "":
		return int(amount), nil
	case unitYears:
		return int(amount) * daysPerYear, nil
	default:
		return 0, &lifecycleError{"unsupported timeUnit " + unit + ", want DAYS or YEARS"}
	}
}

// toLifecycleBody maps the portable rules back onto OCI's shape.
func toLifecycleBody(cfg driver.LifecycleConfig) lifecycleBody {
	out := lifecycleBody{Items: make([]lifecycleRuleBody, 0, len(cfg.Rules))}

	for _, rule := range cfg.Rules {
		item := lifecycleRuleBody{Name: rule.ID, IsEnabled: rule.Enabled, TimeUnit: unitDays}

		switch {
		case rule.ExpirationDays > 0:
			item.Action, item.TimeAmount = lifecycleDelete, int64(rule.ExpirationDays)
		case rule.TransitionDays > 0:
			item.Action, item.TimeAmount = rule.TransitionStorageClass, int64(rule.TransitionDays)
		case rule.AbortMultipartDays > 0:
			item.Action, item.TimeAmount = lifecycleAbortMPU, int64(rule.AbortMultipartDays)
		}

		if rule.Prefix != "" {
			item.ObjectNameFilter = &lifecycleFilterBody{InclusionPrefixes: []string{rule.Prefix}}
		}

		out.Items = append(out.Items, item)
	}

	return out
}

// lifecycleError is a rule the handler rejects before it reaches the driver.
type lifecycleError struct{ msg string }

func (e *lifecycleError) Error() string { return e.msg }

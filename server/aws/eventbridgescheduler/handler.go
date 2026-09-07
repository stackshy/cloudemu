// Package scheduler implements the Amazon EventBridge Scheduler control-plane
// API (restJson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/scheduler client (or the `aws scheduler` CLI, or the
// aws_scheduler_schedule / aws_scheduler_schedule_group Terraform resources) at
// a Server registered with this handler and the schedule, schedule-group and
// tagging operations work end-to-end against an in-memory driver.
//
// This is the standalone EventBridge Scheduler service, distinct from the
// classic EventBridge rules/buses surface. Scheduler routes by HTTP verb + path
// at the root (e.g. POST /schedules/{Name}, GET /schedules/{Name}?groupName=,
// GET /schedule-groups, POST /tags/{arn}); there is no X-Amz-Target header and
// no version prefix. Matches claims the /schedules and /schedule-groups trees
// (unique among the registered handlers) and the /tags paths carrying a
// Scheduler ARN (scoped by the ":scheduler:" marker), so it runs before the S3
// catch-all and never shadows a sibling service's tag operations.
package eventbridgescheduler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// Path roots at the service root.
const (
	rootSchedules      = "schedules"
	rootScheduleGroups = "schedule-groups"
	rootTags           = "tags"
)

// arnMarker scopes the shared /tags root to Scheduler ARNs.
const arnMarker = ":scheduler:"

// Handler serves Amazon EventBridge Scheduler requests against a driver.
type Handler struct {
	s driver.Scheduler
}

// New returns a Scheduler handler backed by d.
func New(d driver.Scheduler) *Handler {
	return &Handler{s: d}
}

// Matches claims the Scheduler path shapes. The /schedules and /schedule-groups
// trees are unique to Scheduler. The /tags root is shared with other restJson1
// services, so it is claimed only for Scheduler ARNs; a non-Scheduler ARN falls
// through.
func (*Handler) Matches(r *http.Request) bool {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootSchedules:
		return len(segs) <= 2 //nolint:mnd // /schedules or /schedules/{name}.
	case rootScheduleGroups:
		return len(segs) <= 2 //nolint:mnd // /schedule-groups or /schedule-groups/{name}.
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	default:
		return false
	}
}

// ServeHTTP dispatches a Scheduler request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(escapedPath(r))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootSchedules:
		h.serveSchedules(w, r, segs[1:])
	case rootScheduleGroups:
		h.serveScheduleGroups(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveSchedules routes GET /schedules (list), POST/GET/PUT/DELETE
// /schedules/{Name}.
func (h *Handler) serveSchedules(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method == http.MethodGet {
			h.listSchedules(w, r)

			return
		}

		methodNotAllowed(w)
	case 1:
		h.serveScheduleItem(w, r, rest[0])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveScheduleItem routes the verb-keyed operations on a single schedule.
func (h *Handler) serveScheduleItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		h.createSchedule(w, r, name)
	case http.MethodGet:
		h.getSchedule(w, r, name)
	case http.MethodPut:
		h.updateSchedule(w, r, name)
	case http.MethodDelete:
		h.deleteSchedule(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

// serveScheduleGroups routes GET /schedule-groups (list), POST/GET/DELETE
// /schedule-groups/{Name}.
func (h *Handler) serveScheduleGroups(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method == http.MethodGet {
			h.listScheduleGroups(w, r)

			return
		}

		methodNotAllowed(w)
	case 1:
		h.serveScheduleGroupItem(w, r, rest[0])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveScheduleGroupItem routes the verb-keyed operations on a single group.
func (h *Handler) serveScheduleGroupItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		h.createScheduleGroup(w, r, name)
	case http.MethodGet:
		h.getScheduleGroup(w, r, name)
	case http.MethodDelete:
		h.deleteScheduleGroup(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

// escapedPath returns the request path preserving percent-encoding so a path
// label that contains a slash (an ARN) survives as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding each
// segment so an ARN or name label is delivered whole to a handler.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))

	for _, seg := range raw {
		if dec, err := url.PathUnescape(seg); err == nil {
			out = append(out, dec)
		} else {
			out = append(out, seg)
		}
	}

	return out
}

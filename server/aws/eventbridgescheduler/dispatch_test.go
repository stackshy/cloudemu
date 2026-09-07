package eventbridgescheduler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/eventbridgescheduler"
)

// TestMatches verifies the handler claims exactly the Scheduler path shapes and
// ARN-scoped /tags paths, and never a sibling service's tag ARN.
func TestMatches(t *testing.T) {
	h := eventbridgescheduler.New(nil)

	const groupArn = "/tags/arn:aws:scheduler:us-east-1:1:schedule-group%2Fg1"

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/schedules", true},                                             // ListSchedules
		{http.MethodPost, "/schedules/s1", true},                                         // CreateSchedule
		{http.MethodGet, "/schedules/s1", true},                                          // GetSchedule
		{http.MethodPut, "/schedules/s1", true},                                          // UpdateSchedule
		{http.MethodDelete, "/schedules/s1", true},                                       // DeleteSchedule
		{http.MethodGet, "/schedule-groups", true},                                       // ListScheduleGroups
		{http.MethodPost, "/schedule-groups/g1", true},                                   // CreateScheduleGroup
		{http.MethodGet, "/schedule-groups/g1", true},                                    // GetScheduleGroup
		{http.MethodDelete, "/schedule-groups/g1", true},                                 // DeleteScheduleGroup
		{http.MethodGet, groupArn, true},                                                 // ListTagsForResource
		{http.MethodPost, groupArn, true},                                                // TagResource
		{http.MethodGet, "/tags/arn:aws:grafana:us-east-1:1:%2Fworkspaces%2Fg-0", false}, // sibling Grafana ARN
		{http.MethodGet, "/tags/arn:aws:aps:us-east-1:1:workspace%2Fws-0", false},        // sibling APS ARN
		{http.MethodGet, "/schedules/s1/extra", false},                                   // deeper path is not an op
		{http.MethodGet, "/some-bucket", false},                                          // arbitrary bucket op
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

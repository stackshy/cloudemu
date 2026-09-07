// Package driver defines the interface and types for the Amazon EventBridge
// Scheduler control-plane API (restJson1). It models standalone schedules and
// the schedule groups that contain them, plus the tags carried by a group.
//
// This is the standalone EventBridge Scheduler service (AWS wire service
// "scheduler"), distinct from the classic EventBridge rules/buses surface
// (services/eventbus) and from GCP Cloud Scheduler (services/scheduler). It is
// control-plane only: the emulator never fires a schedule at its target. A
// schedule and a group are created directly usable, so an IaC apply that reads
// the resource back never blocks. The computed fields clients and IaC tools
// read back — a schedule's Arn, CreationDate and LastModificationDate, and a
// group's Arn, CreationDate, LastModificationDate and State — are minted once at
// create and stored, so repeated GetSchedule/GetScheduleGroup/List reads and a
// later UpdateSchedule never drift. The schedule's Target, FlexibleTimeWindow,
// StartDate and EndDate are carried verbatim as json.RawMessage so their nested
// blocks round-trip exactly as the caller sent them and cannot drift an IaC
// plan.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// DefaultGroupName is the schedule group a schedule joins when the caller names
// none. It always exists implicitly, so GetScheduleGroup("default") reports an
// ACTIVE group even when it was never created.
const DefaultGroupName = "default"

// Schedule state values. A schedule with no explicit state is ENABLED, matching
// the real service default.
const (
	ScheduleEnabled  = "ENABLED"
	ScheduleDisabled = "DISABLED"
)

// Schedule group state values. A group is created directly ACTIVE; DELETING is
// reported for a group whose asynchronous deletion is in progress.
const (
	GroupActive   = "ACTIVE"
	GroupDeleting = "DELETING"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Schedule is a single EventBridge Scheduler schedule. Name and GroupName
// identify it; Arn, CreationDate and LastModificationDate are computed. The
// Target, FlexibleTimeWindow, StartDate and EndDate carry the request body's
// values verbatim so a round-tripped schedule reflects exactly what the caller
// sent.
type Schedule struct {
	Name                       string
	GroupName                  string
	Arn                        string
	ActionAfterCompletion      string
	Description                string
	ScheduleExpression         string
	ScheduleExpressionTimezone string
	State                      string
	KmsKeyArn                  string
	StartDate                  json.RawMessage
	EndDate                    json.RawMessage
	FlexibleTimeWindow         json.RawMessage
	Target                     json.RawMessage
	CreationDate               time.Time
	LastModificationDate       time.Time
}

// ScheduleInput is the input to CreateSchedule and UpdateSchedule. Its JSON tags
// match the request body exactly, so the server unmarshals the request into it
// directly and then sets Name from the URI. UpdateSchedule is a full replacement
// (PUT), so both operations share this shape.
type ScheduleInput struct {
	Name                       string          `json:"-"`
	ActionAfterCompletion      string          `json:"ActionAfterCompletion"`
	ClientToken                string          `json:"ClientToken"`
	Description                string          `json:"Description"`
	EndDate                    json.RawMessage `json:"EndDate"`
	FlexibleTimeWindow         json.RawMessage `json:"FlexibleTimeWindow"`
	GroupName                  string          `json:"GroupName"`
	KmsKeyArn                  string          `json:"KmsKeyArn"`
	ScheduleExpression         string          `json:"ScheduleExpression"`
	ScheduleExpressionTimezone string          `json:"ScheduleExpressionTimezone"`
	StartDate                  json.RawMessage `json:"StartDate"`
	State                      string          `json:"State"`
	Target                     json.RawMessage `json:"Target"`
}

// ScheduleFilter narrows and pages a ListSchedules call. An empty GroupName
// lists across every group.
type ScheduleFilter struct {
	GroupName  string
	NamePrefix string
	State      string
	Page       Page
}

// ScheduleGroup is a container for schedules. Arn, State, CreationDate and
// LastModificationDate are computed once at create and stored, so repeated reads
// never drift.
type ScheduleGroup struct {
	Name                 string
	Arn                  string
	State                string
	CreationDate         time.Time
	LastModificationDate time.Time
	Tags                 map[string]string
}

// GroupFilter narrows and pages a ListScheduleGroups call.
type GroupFilter struct {
	NamePrefix string
	Page       Page
}

// Scheduler is the Amazon EventBridge Scheduler control-plane surface:
// schedules, the groups that contain them, and a group's resource tags.
type Scheduler interface {
	CreateSchedule(ctx context.Context, in *ScheduleInput) (*Schedule, error)
	GetSchedule(ctx context.Context, group, name string) (*Schedule, error)
	UpdateSchedule(ctx context.Context, in *ScheduleInput) (*Schedule, error)
	DeleteSchedule(ctx context.Context, group, name string) error
	ListSchedules(ctx context.Context, filter ScheduleFilter) (schedules []Schedule, nextToken string, err error)

	CreateScheduleGroup(ctx context.Context, name string, tags map[string]string) (*ScheduleGroup, error)
	GetScheduleGroup(ctx context.Context, name string) (*ScheduleGroup, error)
	DeleteScheduleGroup(ctx context.Context, name string) error
	ListScheduleGroups(ctx context.Context, filter GroupFilter) (groups []ScheduleGroup, nextToken string, err error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}

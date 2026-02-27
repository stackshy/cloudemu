// Package iam provides a portable IAM API with cross-cutting concerns.
package iam

import (
	"context"
	"time"

	"github.com/NitinKumar004/cloudemu/iam/driver"
	"github.com/NitinKumar004/cloudemu/inject"
	"github.com/NitinKumar004/cloudemu/metrics"
	"github.com/NitinKumar004/cloudemu/ratelimit"
	"github.com/NitinKumar004/cloudemu/recorder"
)

// IAM is the portable IAM type wrapping a driver.
type IAM struct {
	driver   driver.IAM
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewIAM(d driver.IAM, opts ...Option) *IAM {
	i := &IAM{driver: d}
	for _, opt := range opts { opt(i) }
	return i
}

type Option func(*IAM)

func WithRecorder(r *recorder.Recorder) Option    { return func(i *IAM) { i.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(i *IAM) { i.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(i *IAM) { i.limiter = l } }
func WithErrorInjection(inj *inject.Injector) Option { return func(i *IAM) { i.injector = inj } }
func WithLatency(d time.Duration) Option           { return func(i *IAM) { i.latency = d } }

func (i *IAM) do(ctx context.Context, op string, input interface{}, fn func() (interface{}, error)) (interface{}, error) {
	start := time.Now()
	if i.injector != nil { if err := i.injector.Check("iam", op); err != nil { i.rec(op, input, nil, err, time.Since(start)); return nil, err } }
	if i.limiter != nil { if err := i.limiter.Allow(); err != nil { i.rec(op, input, nil, err, time.Since(start)); return nil, err } }
	if i.latency > 0 { time.Sleep(i.latency) }
	out, err := fn()
	dur := time.Since(start)
	if i.metrics != nil {
		labels := map[string]string{"service": "iam", "operation": op}
		i.metrics.Counter("calls_total", 1, labels)
		i.metrics.Histogram("call_duration", dur, labels)
		if err != nil { i.metrics.Counter("errors_total", 1, labels) }
	}
	i.rec(op, input, out, err, dur)
	return out, err
}

func (i *IAM) rec(op string, input, output interface{}, err error, dur time.Duration) {
	if i.recorder != nil { i.recorder.Record("iam", op, input, output, err, dur) }
}

func (i *IAM) CreateUser(ctx context.Context, config driver.UserConfig) (*driver.UserInfo, error) {
	out, err := i.do(ctx, "CreateUser", config, func() (interface{}, error) { return i.driver.CreateUser(ctx, config) })
	if err != nil { return nil, err }
	return out.(*driver.UserInfo), nil
}
func (i *IAM) DeleteUser(ctx context.Context, name string) error { _, err := i.do(ctx, "DeleteUser", name, func() (interface{}, error) { return nil, i.driver.DeleteUser(ctx, name) }); return err }
func (i *IAM) GetUser(ctx context.Context, name string) (*driver.UserInfo, error) {
	out, err := i.do(ctx, "GetUser", name, func() (interface{}, error) { return i.driver.GetUser(ctx, name) })
	if err != nil { return nil, err }
	return out.(*driver.UserInfo), nil
}
func (i *IAM) ListUsers(ctx context.Context) ([]driver.UserInfo, error) {
	out, err := i.do(ctx, "ListUsers", nil, func() (interface{}, error) { return i.driver.ListUsers(ctx) })
	if err != nil { return nil, err }
	return out.([]driver.UserInfo), nil
}
func (i *IAM) CreateRole(ctx context.Context, config driver.RoleConfig) (*driver.RoleInfo, error) {
	out, err := i.do(ctx, "CreateRole", config, func() (interface{}, error) { return i.driver.CreateRole(ctx, config) })
	if err != nil { return nil, err }
	return out.(*driver.RoleInfo), nil
}
func (i *IAM) DeleteRole(ctx context.Context, name string) error { _, err := i.do(ctx, "DeleteRole", name, func() (interface{}, error) { return nil, i.driver.DeleteRole(ctx, name) }); return err }
func (i *IAM) GetRole(ctx context.Context, name string) (*driver.RoleInfo, error) {
	out, err := i.do(ctx, "GetRole", name, func() (interface{}, error) { return i.driver.GetRole(ctx, name) })
	if err != nil { return nil, err }
	return out.(*driver.RoleInfo), nil
}
func (i *IAM) ListRoles(ctx context.Context) ([]driver.RoleInfo, error) {
	out, err := i.do(ctx, "ListRoles", nil, func() (interface{}, error) { return i.driver.ListRoles(ctx) })
	if err != nil { return nil, err }
	return out.([]driver.RoleInfo), nil
}
func (i *IAM) CreatePolicy(ctx context.Context, config driver.PolicyConfig) (*driver.PolicyInfo, error) {
	out, err := i.do(ctx, "CreatePolicy", config, func() (interface{}, error) { return i.driver.CreatePolicy(ctx, config) })
	if err != nil { return nil, err }
	return out.(*driver.PolicyInfo), nil
}
func (i *IAM) DeletePolicy(ctx context.Context, arn string) error { _, err := i.do(ctx, "DeletePolicy", arn, func() (interface{}, error) { return nil, i.driver.DeletePolicy(ctx, arn) }); return err }
func (i *IAM) GetPolicy(ctx context.Context, arn string) (*driver.PolicyInfo, error) {
	out, err := i.do(ctx, "GetPolicy", arn, func() (interface{}, error) { return i.driver.GetPolicy(ctx, arn) })
	if err != nil { return nil, err }
	return out.(*driver.PolicyInfo), nil
}
func (i *IAM) ListPolicies(ctx context.Context) ([]driver.PolicyInfo, error) {
	out, err := i.do(ctx, "ListPolicies", nil, func() (interface{}, error) { return i.driver.ListPolicies(ctx) })
	if err != nil { return nil, err }
	return out.([]driver.PolicyInfo), nil
}
func (i *IAM) AttachUserPolicy(ctx context.Context, userName, policyARN string) error { _, err := i.do(ctx, "AttachUserPolicy", userName, func() (interface{}, error) { return nil, i.driver.AttachUserPolicy(ctx, userName, policyARN) }); return err }
func (i *IAM) DetachUserPolicy(ctx context.Context, userName, policyARN string) error { _, err := i.do(ctx, "DetachUserPolicy", userName, func() (interface{}, error) { return nil, i.driver.DetachUserPolicy(ctx, userName, policyARN) }); return err }
func (i *IAM) AttachRolePolicy(ctx context.Context, roleName, policyARN string) error { _, err := i.do(ctx, "AttachRolePolicy", roleName, func() (interface{}, error) { return nil, i.driver.AttachRolePolicy(ctx, roleName, policyARN) }); return err }
func (i *IAM) DetachRolePolicy(ctx context.Context, roleName, policyARN string) error { _, err := i.do(ctx, "DetachRolePolicy", roleName, func() (interface{}, error) { return nil, i.driver.DetachRolePolicy(ctx, roleName, policyARN) }); return err }
func (i *IAM) ListAttachedUserPolicies(ctx context.Context, userName string) ([]string, error) {
	out, err := i.do(ctx, "ListAttachedUserPolicies", userName, func() (interface{}, error) { return i.driver.ListAttachedUserPolicies(ctx, userName) })
	if err != nil { return nil, err }
	return out.([]string), nil
}
func (i *IAM) ListAttachedRolePolicies(ctx context.Context, roleName string) ([]string, error) {
	out, err := i.do(ctx, "ListAttachedRolePolicies", roleName, func() (interface{}, error) { return i.driver.ListAttachedRolePolicies(ctx, roleName) })
	if err != nil { return nil, err }
	return out.([]string), nil
}
func (i *IAM) CheckPermission(ctx context.Context, principal, action, resource string) (bool, error) {
	out, err := i.do(ctx, "CheckPermission", principal, func() (interface{}, error) { return i.driver.CheckPermission(ctx, principal, action, resource) })
	if err != nil { return false, err }
	return out.(bool), nil
}

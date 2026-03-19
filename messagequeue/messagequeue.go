// Package messagequeue provides a portable message queue API with cross-cutting concerns.
package messagequeue

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/messagequeue/driver"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

type MQ struct {
	driver   driver.MessageQueue
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewMQ(d driver.MessageQueue, opts ...Option) *MQ {
	mq := &MQ{driver: d}
	for _, opt := range opts {
		opt(mq)
	}
	return mq
}

type Option func(*MQ)

func WithRecorder(r *recorder.Recorder) Option     { return func(mq *MQ) { mq.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(mq *MQ) { mq.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(mq *MQ) { mq.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(mq *MQ) { mq.injector = i } }
func WithLatency(d time.Duration) Option           { return func(mq *MQ) { mq.latency = d } }

func (mq *MQ) do(ctx context.Context, op string, input interface{}, fn func() (interface{}, error)) (interface{}, error) {
	start := time.Now()
	if mq.injector != nil {
		if err := mq.injector.Check("messagequeue", op); err != nil {
			mq.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}
	if mq.limiter != nil {
		if err := mq.limiter.Allow(); err != nil {
			mq.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}
	if mq.latency > 0 {
		time.Sleep(mq.latency)
	}
	out, err := fn()
	dur := time.Since(start)
	if mq.metrics != nil {
		labels := map[string]string{"service": "messagequeue", "operation": op}
		mq.metrics.Counter("calls_total", 1, labels)
		mq.metrics.Histogram("call_duration", dur, labels)
		if err != nil {
			mq.metrics.Counter("errors_total", 1, labels)
		}
	}
	mq.rec(op, input, out, err, dur)
	return out, err
}

func (mq *MQ) rec(op string, input, output interface{}, err error, dur time.Duration) {
	if mq.recorder != nil {
		mq.recorder.Record("messagequeue", op, input, output, err, dur)
	}
}

func (mq *MQ) CreateQueue(ctx context.Context, config driver.QueueConfig) (*driver.QueueInfo, error) {
	out, err := mq.do(ctx, "CreateQueue", config, func() (interface{}, error) { return mq.driver.CreateQueue(ctx, config) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.QueueInfo), nil
}
func (mq *MQ) DeleteQueue(ctx context.Context, url string) error {
	_, err := mq.do(ctx, "DeleteQueue", url, func() (interface{}, error) { return nil, mq.driver.DeleteQueue(ctx, url) })
	return err
}
func (mq *MQ) GetQueueInfo(ctx context.Context, url string) (*driver.QueueInfo, error) {
	out, err := mq.do(ctx, "GetQueueInfo", url, func() (interface{}, error) { return mq.driver.GetQueueInfo(ctx, url) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.QueueInfo), nil
}
func (mq *MQ) ListQueues(ctx context.Context, prefix string) ([]driver.QueueInfo, error) {
	out, err := mq.do(ctx, "ListQueues", prefix, func() (interface{}, error) { return mq.driver.ListQueues(ctx, prefix) })
	if err != nil {
		return nil, err
	}
	return out.([]driver.QueueInfo), nil
}
func (mq *MQ) SendMessage(ctx context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	out, err := mq.do(ctx, "SendMessage", input, func() (interface{}, error) { return mq.driver.SendMessage(ctx, input) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.SendMessageOutput), nil
}
func (mq *MQ) ReceiveMessages(ctx context.Context, input driver.ReceiveMessageInput) ([]driver.Message, error) {
	out, err := mq.do(ctx, "ReceiveMessages", input, func() (interface{}, error) { return mq.driver.ReceiveMessages(ctx, input) })
	if err != nil {
		return nil, err
	}
	return out.([]driver.Message), nil
}
func (mq *MQ) DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error {
	_, err := mq.do(ctx, "DeleteMessage", queueURL, func() (interface{}, error) { return nil, mq.driver.DeleteMessage(ctx, queueURL, receiptHandle) })
	return err
}
func (mq *MQ) ChangeVisibility(ctx context.Context, queueURL, receiptHandle string, timeout int) error {
	_, err := mq.do(ctx, "ChangeVisibility", queueURL, func() (interface{}, error) {
		return nil, mq.driver.ChangeVisibility(ctx, queueURL, receiptHandle, timeout)
	})
	return err
}

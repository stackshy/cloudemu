// Package testhelper provides test suite helpers for cloudemu.
package testhelper

import (
	"github.com/NitinKumar004/cloudemu/config"
	"github.com/NitinKumar004/cloudemu/inject"
	"github.com/NitinKumar004/cloudemu/metrics"
	"github.com/NitinKumar004/cloudemu/providers/aws"
	"github.com/NitinKumar004/cloudemu/providers/azure"
	"github.com/NitinKumar004/cloudemu/providers/gcp"
	"github.com/NitinKumar004/cloudemu/recorder"
)

// Suite holds shared test infrastructure.
type Suite struct {
	Recorder *recorder.Recorder
	Metrics  *metrics.Collector
	Injector *inject.Injector
	Clock    *config.FakeClock
}

// NewSuite creates a new test suite with shared infrastructure.
func NewSuite() *Suite {
	return &Suite{
		Recorder: recorder.New(),
		Metrics:  metrics.NewCollector(),
		Injector: inject.NewInjector(),
		Clock:    config.NewFakeClock(config.RealClock{}.Now()),
	}
}

// Reset resets all shared infrastructure.
func (s *Suite) Reset() {
	s.Recorder.Reset()
	s.Metrics.Reset()
	s.Injector.Reset()
}

// AWSProvider creates an AWS provider configured with the suite's clock.
func (s *Suite) AWSProvider(opts ...config.Option) *aws.Provider {
	allOpts := append([]config.Option{config.WithClock(s.Clock)}, opts...)
	return aws.New(allOpts...)
}

// AzureProvider creates an Azure provider configured with the suite's clock.
func (s *Suite) AzureProvider(opts ...config.Option) *azure.Provider {
	allOpts := append([]config.Option{config.WithClock(s.Clock)}, opts...)
	return azure.New(allOpts...)
}

// GCPProvider creates a GCP provider configured with the suite's clock.
func (s *Suite) GCPProvider(opts ...config.Option) *gcp.Provider {
	allOpts := append([]config.Option{config.WithClock(s.Clock)}, opts...)
	return gcp.New(allOpts...)
}

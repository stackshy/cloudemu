// Package gcp provides GCP mock provider factories.
package gcp

import (
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/providers/gcp/clouddns"
	"github.com/stackshy/cloudemu/providers/gcp/cloudfunctions"
	"github.com/stackshy/cloudemu/providers/gcp/cloudmonitoring"
	"github.com/stackshy/cloudemu/providers/gcp/firestore"
	"github.com/stackshy/cloudemu/providers/gcp/gce"
	"github.com/stackshy/cloudemu/providers/gcp/gcpiam"
	"github.com/stackshy/cloudemu/providers/gcp/gcplb"
	"github.com/stackshy/cloudemu/providers/gcp/gcpvpc"
	"github.com/stackshy/cloudemu/providers/gcp/gcs"
	"github.com/stackshy/cloudemu/providers/gcp/pubsub"
)

// Provider holds all GCP mock services.
type Provider struct {
	GCS             *gcs.Mock
	GCE             *gce.Mock
	Firestore       *firestore.Mock
	CloudFunctions  *cloudfunctions.Mock
	VPC             *gcpvpc.Mock
	CloudMonitoring *cloudmonitoring.Mock
	IAM             *gcpiam.Mock
	CloudDNS        *clouddns.Mock
	LB              *gcplb.Mock
	PubSub          *pubsub.Mock
}

// New creates a new GCP provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		GCS:             gcs.New(o),
		GCE:             gce.New(o),
		Firestore:       firestore.New(o),
		CloudFunctions:  cloudfunctions.New(o),
		VPC:             gcpvpc.New(o),
		CloudMonitoring: cloudmonitoring.New(o),
		IAM:             gcpiam.New(o),
		CloudDNS:        clouddns.New(o),
		LB:              gcplb.New(o),
		PubSub:          pubsub.New(o),
	}
	p.GCE.SetMonitoring(p.CloudMonitoring)

	return p
}

package vpc

import (
	"context"
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// autoCreatedSubnetTag marks a subnetwork the emulator created for an auto
// mode network, so deleting the network removes it with the network.
const autoCreatedSubnetTag = "cloudemu:gcpAutoCreated"

// autoModeRanges is GCP's published auto mode IPv4 plan: one /20 per region
// carved from 10.128.0.0/9 (cloud.google.com/vpc/docs/subnets#ip-ranges).
var autoModeRanges = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"us-central1":             "10.128.0.0/20",
	"europe-west1":            "10.132.0.0/20",
	"us-west1":                "10.138.0.0/20",
	"asia-east1":              "10.140.0.0/20",
	"us-east1":                "10.142.0.0/20",
	"asia-northeast1":         "10.146.0.0/20",
	"asia-southeast1":         "10.148.0.0/20",
	"us-east4":                "10.150.0.0/20",
	"australia-southeast1":    "10.152.0.0/20",
	"europe-west2":            "10.154.0.0/20",
	"europe-west3":            "10.156.0.0/20",
	"southamerica-east1":      "10.158.0.0/20",
	"asia-south1":             "10.160.0.0/20",
	"northamerica-northeast1": "10.162.0.0/20",
	"europe-west4":            "10.164.0.0/20",
	"europe-north1":           "10.166.0.0/20",
	"us-west2":                "10.168.0.0/20",
	"asia-east2":              "10.170.0.0/20",
	"europe-west6":            "10.172.0.0/20",
	"asia-northeast2":         "10.174.0.0/20",
	"asia-northeast3":         "10.178.0.0/20",
	"us-west3":                "10.180.0.0/20",
	"us-west4":                "10.182.0.0/20",
	"asia-southeast2":         "10.184.0.0/20",
	"europe-central2":         "10.186.0.0/20",
	"northamerica-northeast2": "10.188.0.0/20",
	"asia-south2":             "10.190.0.0/20",
	"australia-southeast2":    "10.192.0.0/20",
}

// createAutoSubnets creates the per-region subnetworks of an auto mode network,
// each named after the network, as real GCP does when autoCreateSubnetworks is
// true.
func (h *Handler) createAutoSubnets(ctx context.Context, vpcID, netName string) error {
	regions := make([]string, 0, len(autoModeRanges))
	for region := range autoModeRanges {
		regions = append(regions, region)
	}

	sort.Strings(regions)

	for _, region := range regions {
		cfg := netdriver.SubnetConfig{
			VPCID:            vpcID,
			CIDRBlock:        autoModeRanges[region],
			AvailabilityZone: region,
			Tags: map[string]string{
				subnetNameTag:        netName,
				subnetNetworkTag:     netName,
				createdAtTag:         nowRFC3339(),
				autoCreatedSubnetTag: trueValue,
			},
		}

		if _, err := h.net.CreateSubnet(ctx, cfg); err != nil {
			return err
		}
	}

	return nil
}

// releaseAutoSubnets deletes an auto mode network's own subnetworks ahead of
// the network, as GCP does when the network is deleted. It deletes nothing
// while another child (a custom subnetwork or a firewall) still blocks the
// network, leaving the provider to reject the delete, and answers 400
// resourceInUseByAnotherResource while an instance sits in an auto subnetwork.
// It reports whether the caller may go on to delete the network.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) releaseAutoSubnets(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, vpcID string) bool {
	ctx := r.Context()

	auto, err := h.releasableAutoSubnets(ctx, vpcID)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return false
	}

	for i := range auto {
		inst, err := h.instanceInSubnet(ctx, hostOf(r), rp.Project, rp.ResourceName, auto[i].AvailabilityZone)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return false
		}

		if inst != "" {
			gcprest.WriteError(w, http.StatusBadRequest, "resourceInUseByAnotherResource",
				"The network resource '"+rp.ResourceName+"' is already being used by '"+inst+"'")

			return false
		}
	}

	for i := range auto {
		if err := h.net.DeleteSubnet(ctx, auto[i].ID); err != nil {
			gcprest.WriteCErr(w, err)
			return false
		}
	}

	return true
}

// releasableAutoSubnets returns the network's auto subnetworks, or none when a
// custom subnetwork or a firewall still uses the network.
func (h *Handler) releasableAutoSubnets(ctx context.Context, vpcID string) ([]netdriver.SubnetInfo, error) {
	subnets, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}

	var auto []netdriver.SubnetInfo

	for i := range subnets {
		if subnets[i].VPCID != vpcID {
			continue
		}

		if subnets[i].Tags[autoCreatedSubnetTag] != trueValue {
			return nil, nil
		}

		auto = append(auto, subnets[i])
	}

	fws, err := h.net.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range fws {
		if fws[i].VPCID == vpcID {
			return nil, nil
		}
	}

	return auto, nil
}

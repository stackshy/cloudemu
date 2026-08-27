package compute

import (
	"strconv"
	"strings"

	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// defaultCPUPlatform is the CPU platform GCP reports for a running VM.
const defaultCPUPlatform = "Intel Broadwell"

// toInstanceResponse maps a driver Instance back to GCP's REST shape, resolving
// the GCP-specific state carried in the instance's internal tags (disks,
// network tags, metadata, network self-link) and filling realistic defaults.
func toInstanceResponse(inst *computedriver.Instance, project, host string) instanceResponse {
	name := tagOr(inst.Tags, gcpNameTag, "")
	zone := tagOr(inst.Tags, keyZone, "")
	labels := userLabels(inst.Tags)
	netTags := decodeNetTags(inst.Tags)
	metaItems := decodeMetadata(inst.Tags)

	resp := instanceResponse{
		Kind:               "compute#instance",
		ID:                 numericID(inst.ID),
		Name:               name,
		MachineType:        zoneLink(host, project, zone, "machineTypes", inst.InstanceType),
		Status:             gcpStatusFor(inst.State),
		Zone:               host + "/compute/v1/projects/" + project + "/zones/" + zone,
		SelfLink:           zoneLink(host, project, zone, "instances", name),
		CreationTimestamp:  inst.LaunchTime,
		CPUPlatform:        defaultCPUPlatform,
		DeletionProtection: false,
		Disks:              resolveDisks(inst, host, project, zone, name),
		NetworkInterfaces:  instanceNICs(inst, host, project, zone),
		Labels:             labels,
		LabelFingerprint:   labelFingerprintFor(labels),
		Fingerprint:        fingerprint(inst.ID, inst.InstanceType, inst.State),
		Tags: &tagsBlock{
			Items:       netTags,
			Fingerprint: fingerprint(strings.Join(netTags, ",")),
		},
		Metadata:               metadataResponse(metaItems),
		Scheduling:             defaultScheduling(),
		ServiceAccounts:        serviceAccountsFor(inst.Tags),
		ShieldedInstanceConfig: defaultShieldedConfig(),
	}

	if resp.Status == statusRunning {
		resp.LastStartTimestamp = inst.LaunchTime
	}

	return resp
}

// zoneLink builds a zone-scoped self-link. Used instead of gcprest.SelfLink so
// list/aggregatedList responses carry each instance's own zone rather than the
// request path's scope (aggregatedList has no zone in its path).
func zoneLink(host, project, zone, resourceType, name string) string {
	host = strings.TrimSuffix(host, "/")
	return host + "/compute/v1/projects/" + project + "/zones/" + zone + "/" + resourceType + "/" + name
}

// resolveDisks turns the stored inbound disk descriptors into the full GCP
// attachedDisk read shape (resolved source self-link, diskSizeGb, type, mode).
func resolveDisks(inst *computedriver.Instance, host, project, zone, instanceName string) []attachedDisk {
	disks := decodeDisks(inst.Tags)
	if len(disks) == 0 {
		return nil
	}

	out := make([]attachedDisk, 0, len(disks))

	for i := range disks {
		d := disks[i]
		d.Kind = "compute#attachedDisk"
		d.Index = i

		if d.DeviceName == "" {
			d.DeviceName = deviceNameFor(d.Boot, instanceName, i)
		}

		if d.Type == "" {
			d.Type = "PERSISTENT"
		}

		if d.Mode == "" {
			d.Mode = "READ_WRITE"
		}

		if d.DiskSizeGb == "" {
			d.DiskSizeGb = diskSizeFor(&d)
		}

		if d.Source == "" {
			d.Source = zoneLink(host, project, zone, "disks", d.DeviceName)
		}

		out = append(out, d)
	}

	return out
}

func deviceNameFor(boot bool, instanceName string, index int) string {
	if boot {
		return instanceName
	}

	return "persistent-disk-" + strconv.Itoa(index)
}

func diskSizeFor(d *attachedDisk) string {
	if d.InitializeParams != nil && d.InitializeParams.DiskSizeGb != "" {
		return d.InitializeParams.DiskSizeGb
	}

	return defaultDiskSizeGb
}

// instanceNICs echoes the instance's network interface with fully-qualified
// network and subnetwork self-links (real GCP resolves both to absolute URLs).
func instanceNICs(inst *computedriver.Instance, host, project, zone string) []networkInterface {
	network := qualifyNetwork(host, project, tagOr(inst.Tags, keyNetwork, ""))
	subnet := qualifySubnetwork(host, project, zone, inst.SubnetID)
	accessConfigs := decodeAccessConfigs(inst.Tags)

	if network == "" && subnet == "" && inst.PrivateIP == "" && len(accessConfigs) == 0 {
		return nil
	}

	for i := range accessConfigs {
		accessConfigs[i].Kind = "compute#accessConfig"
	}

	return []networkInterface{{
		Name:          "nic0",
		Network:       network,
		Subnetwork:    subnet,
		NetworkIP:     inst.PrivateIP,
		StackType:     "IPV4_ONLY",
		AccessConfigs: accessConfigs,
	}}
}

// qualifyNetwork resolves a raw network reference to a global self-link.
func qualifyNetwork(host, project, raw string) string {
	if raw == "" {
		return ""
	}

	host = strings.TrimSuffix(host, "/")

	return host + "/compute/v1/projects/" + project + "/global/networks/" + lastSegment(raw)
}

// qualifySubnetwork resolves a raw subnetwork reference to a region-scoped
// self-link, inferring the region from the zone when the raw value omits it.
func qualifySubnetwork(host, project, zone, raw string) string {
	if raw == "" {
		return ""
	}

	host = strings.TrimSuffix(host, "/")
	region, name := parseSubnetRef(raw, zone)

	return host + "/compute/v1/projects/" + project + "/regions/" + region + "/subnetworks/" + name
}

// parseSubnetRef pulls the region and subnetwork name out of a raw reference of
// the form ".../regions/{r}/subnetworks/{n}" or a bare name.
func parseSubnetRef(raw, zone string) (region, name string) {
	parts := strings.Split(raw, "/")
	region = regionFromZone(zone)
	name = parts[len(parts)-1]

	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "regions" {
			region = parts[i+1]
		}
	}

	return region, name
}

// regionFromZone derives the region ("us-central1") from a zone
// ("us-central1-a") by trimming the trailing "-<letter>".
func regionFromZone(zone string) string {
	if i := strings.LastIndex(zone, "-"); i > 0 {
		return zone[:i]
	}

	return zone
}

func metadataResponse(items []metadataItem) *metadataBlock {
	joined := make([]string, 0, len(items)*kvStride)
	for _, it := range items {
		joined = append(joined, it.Key, it.Value)
	}

	return &metadataBlock{
		Kind:        "compute#metadata",
		Items:       items,
		Fingerprint: fingerprint(joined...),
	}
}

func defaultScheduling() *scheduling {
	return &scheduling{
		AutomaticRestart:  true,
		OnHostMaintenance: "MIGRATE",
		Preemptible:       false,
		ProvisioningModel: "STANDARD",
	}
}

// serviceAccountsFor reflects the serviceAccounts[] the client attached at
// insert time (email + scopes, round-tripped exactly). When the client omitted
// them, it falls back to the default compute service account real GCP attaches.
func serviceAccountsFor(tags map[string]string) []serviceAccount {
	if sas := decodeServiceAccounts(tags); len(sas) > 0 {
		return sas
	}

	return defaultServiceAccounts()
}

func defaultServiceAccounts() []serviceAccount {
	return []serviceAccount{{
		Email:  "default",
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	}}
}

func defaultShieldedConfig() *shieldedInstanceConfig {
	return &shieldedInstanceConfig{
		EnableSecureBoot:          false,
		EnableVtpm:                true,
		EnableIntegrityMonitoring: true,
	}
}

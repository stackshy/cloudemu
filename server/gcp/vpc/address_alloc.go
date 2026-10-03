package vpc

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/ipalloc"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	addressTypeExternal = "EXTERNAL"
	addressTypeInternal = "INTERNAL"
	defaultNetworkTier  = "PREMIUM"

	// publicIPAttempts bounds the re-hashing done to dodge a public IP another
	// address of the project already holds.
	publicIPAttempts = 16
	// firstPublicOctet is the first octet of the 34.0.0.0/8 and 35.0.0.0/8
	// blocks external addresses come from.
	firstPublicOctet = 34
)

// assignAddress settles the IP and type fields of an address being reserved,
// the way compute.addresses.insert does. An EXTERNAL address (the default)
// gets a public IP and reads back addressType EXTERNAL with networkTier
// PREMIUM unless the caller chose a tier. An INTERNAL address in a subnetwork
// takes its IP from the subnet (see assignInternal). Other INTERNAL addresses
// (a private services access range, for one) keep the provider's synthetic
// allocator.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) assignAddress(ctx context.Context, rp gcprest.ResourcePath, host, name string,
	body map[string]any,
) error {
	addrType, _ := body["addressType"].(string)
	if addrType == "" {
		addrType = addressTypeExternal
		body["addressType"] = addrType
	}

	ip, _ := body["address"].(string)

	if addrType == addressTypeExternal {
		if tier, _ := body["networkTier"].(string); tier == "" {
			body["networkTier"] = defaultNetworkTier
		}

		if ip == "" {
			body["address"] = h.publicIP(ctx, rp, name)
		}

		return nil
	}

	subnetRef, _ := body["subnetwork"].(string)
	if addrType != addressTypeInternal || subnetRef == "" || rp.Scope != gcprest.ScopeRegions {
		return nil
	}

	return h.assignInternal(ctx, rp, host, subnetRef, ip, body)
}

// assignInternal gives an INTERNAL address a free IP of its subnetwork's
// range, or keeps the caller's IP when it is inside the range and free. A
// missing subnetwork is NotFound, and an IP outside the range or already taken
// is InvalidArgument.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) assignInternal(ctx context.Context, rp gcprest.ResourcePath, host, subnetRef, ip string,
	body map[string]any,
) error {
	region, subnetName := subnetRefParts(subnetRef, rp.ScopeName)
	project := refProject(subnetRef, rp.Project)

	subnet, err := findSubnetByName(projectctx.WithProject(ctx, project), h.net, subnetName, region)
	if err != nil {
		return cerrors.Newf(cerrors.NotFound,
			"The resource 'projects/%s/regions/%s/subnetworks/%s' was not found", project, region, subnetName)
	}

	used := h.usedIPsInSubnet(ctx, project, subnetName, region)

	switch {
	case ip == "":
		ip = ipalloc.FirstFree(subnet.CIDRBlock, used)
		if ip == "" {
			return cerrors.Newf(cerrors.ResourceExhausted, "subnetwork %s has no free IP addresses", subnetName)
		}
	case !ipalloc.Usable(subnet.CIDRBlock, ip):
		return cerrors.Newf(cerrors.InvalidArgument,
			"Requested internal IP address '%s' is outside the subnetwork '%s' range %s", ip, subnetName, subnet.CIDRBlock)
	case used[ip]:
		return cerrors.Newf(cerrors.InvalidArgument, "IP '%s' is already being used by another resource", ip)
	}

	body["address"] = ip
	body["subnetwork"] = gcprest.SelfLink(host, project, gcprest.ScopeRegions, region, resourceSubnetworks, subnetName)

	return nil
}

// subnetRefParts returns the region and name of a subnetwork reference,
// defaulting the region to fallback for a bare name.
func subnetRefParts(ref, fallback string) (region, name string) {
	parts := strings.Split(ref, "/")
	region, name = fallback, parts[len(parts)-1]

	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "regions" {
			region = parts[i+1]
		}
	}

	return region, name
}

// refProject returns the project a resource reference names, or fallback for
// a bare name or a relative reference with no project.
func refProject(ref, fallback string) string {
	if p := projectctx.FromPath(ref); p != "" {
		return p
	}

	return fallback
}

// usedIPsInSubnet collects the IPs taken in a subnetwork: those of instances
// launched into it and those of addresses reserved in it.
func (h *Handler) usedIPsInSubnet(ctx context.Context, project, name, region string) map[string]bool {
	used := h.instanceIPsInSubnet(ctx, project, name, region)

	for _, raw := range h.addresses.list(ctx, project, region) {
		var a struct {
			Address    string `json:"address"`
			Subnetwork string `json:"subnetwork"`
		}

		if json.Unmarshal(raw, &a) == nil && a.Address != "" && lastSegment(a.Subnetwork) == name {
			used[a.Address] = true
		}
	}

	return used
}

// instanceIPsInSubnet collects the private IPs of instances in a subnetwork.
// Every project is scanned, because Shared VPC instances of other projects
// draw from the same subnet.
func (h *Handler) instanceIPsInSubnet(ctx context.Context, project, name, region string) map[string]bool {
	used := map[string]bool{}

	if h.compute == nil {
		return used
	}

	instances, err := h.compute.DescribeInstances(projectctx.AllProjects(ctx), nil, nil)
	if err != nil {
		return used
	}

	for i := range instances {
		inst := &instances[i]
		if inst.PrivateIP != "" && subnetRefMatches(inst.SubnetID, name, region) &&
			refProject(inst.SubnetID, tagOr(inst.Tags, instProjectTag, project)) == project {
			used[inst.PrivateIP] = true
		}
	}

	return used
}

// publicIP derives a stable public-looking IPv4 for an external address from
// its identity, in 34.0.0.0/8 or 35.0.0.0/8 where GCP's external addresses
// live, re-hashing past any IP another address of the project already holds.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) publicIP(ctx context.Context, rp gcprest.ResourcePath, name string) string {
	taken := map[string]bool{}

	for _, bodies := range h.addresses.allByScope(ctx, rp.Project) {
		for _, b := range bodies {
			taken[addressIP(b)] = true
		}
	}

	var ip string

	for attempt := 0; attempt < publicIPAttempts; attempt++ {
		f := fnv.New32a()
		_, _ = f.Write([]byte(rp.Project + "/" + scopeOf(rp) + "/" + name + "#" + strconv.Itoa(attempt)))

		const octetMod = 254

		v := f.Sum32()
		ip = strconv.Itoa(firstPublicOctet+int(v&1)) + "." + strconv.Itoa(int(v%octetMod)+1) + "." +
			strconv.Itoa(int((v>>8)%octetMod)+1) + "." + strconv.Itoa(int((v>>16)%octetMod)+1)

		if !taken[ip] {
			break
		}
	}

	return ip
}

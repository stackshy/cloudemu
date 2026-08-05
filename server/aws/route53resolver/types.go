package route53resolver

import "github.com/stackshy/cloudemu/v2/services/route53resolver/driver"

// --- wire shapes (AWS JSON 1.1 member names, PascalCase, in the json tags) ---

type wireTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type wireIPAddressRequest struct {
	SubnetID string `json:"SubnetId"`
	IP       string `json:"Ip"`
	IPv6     string `json:"Ipv6"`
}

type wireIPAddressUpdate struct {
	IPID     string `json:"IpId"`
	SubnetID string `json:"SubnetId"`
	IP       string `json:"Ip"`
	IPv6     string `json:"Ipv6"`
}

type wireIPAddressResponse struct {
	IPID             string `json:"IpId,omitempty"`
	SubnetID         string `json:"SubnetId,omitempty"`
	IP               string `json:"Ip,omitempty"`
	IPv6             string `json:"Ipv6,omitempty"`
	Status           string `json:"Status,omitempty"`
	StatusMessage    string `json:"StatusMessage,omitempty"`
	CreationTime     string `json:"CreationTime,omitempty"`
	ModificationTime string `json:"ModificationTime,omitempty"`
}

type wireResolverEndpoint struct {
	ID                        string   `json:"Id,omitempty"`
	Arn                       string   `json:"Arn,omitempty"`
	Name                      string   `json:"Name,omitempty"`
	CreatorRequestID          string   `json:"CreatorRequestId,omitempty"`
	Direction                 string   `json:"Direction,omitempty"`
	HostVPCID                 string   `json:"HostVPCId,omitempty"`
	IPAddressCount            int32    `json:"IpAddressCount,omitempty"`
	SecurityGroupIDs          []string `json:"SecurityGroupIds,omitempty"`
	Status                    string   `json:"Status,omitempty"`
	StatusMessage             string   `json:"StatusMessage,omitempty"`
	ResolverEndpointType      string   `json:"ResolverEndpointType,omitempty"`
	Protocols                 []string `json:"Protocols,omitempty"`
	OutpostArn                string   `json:"OutpostArn,omitempty"`
	PreferredInstanceType     string   `json:"PreferredInstanceType,omitempty"`
	DNS64Enabled              bool     `json:"Dns64Enabled,omitempty"`
	IPv6InternetAccessEnabled bool     `json:"Ipv6InternetAccessEnabled,omitempty"`
	CreationTime              string   `json:"CreationTime,omitempty"`
	ModificationTime          string   `json:"ModificationTime,omitempty"`
}

// --- mapping: driver <-> wire ---

func endpointToWire(e *driver.ResolverEndpoint) wireResolverEndpoint {
	return wireResolverEndpoint{
		ID:                        e.ID,
		Arn:                       e.ARN,
		Name:                      e.Name,
		CreatorRequestID:          e.CreatorRequestID,
		Direction:                 e.Direction,
		HostVPCID:                 e.HostVPCID,
		IPAddressCount:            e.IPAddressCount,
		SecurityGroupIDs:          e.SecurityGroupIDs,
		Status:                    e.Status,
		StatusMessage:             e.StatusMessage,
		ResolverEndpointType:      e.ResolverEndpointType,
		Protocols:                 e.Protocols,
		OutpostArn:                e.OutpostARN,
		PreferredInstanceType:     e.PreferredInstanceType,
		DNS64Enabled:              e.DNS64Enabled,
		IPv6InternetAccessEnabled: e.IPv6InternetAccessEnabled,
		CreationTime:              e.CreatedAt,
		ModificationTime:          e.ModifiedAt,
	}
}

func ipToWire(ip *driver.IPAddress) wireIPAddressResponse {
	return wireIPAddressResponse{
		IPID:             ip.IPID,
		SubnetID:         ip.SubnetID,
		IP:               ip.IP,
		IPv6:             ip.IPv6,
		Status:           ip.Status,
		CreationTime:     ip.CreatedAt,
		ModificationTime: ip.ModifiedAt,
	}
}

func ipsToWire(ips []driver.IPAddress) []wireIPAddressResponse {
	out := make([]wireIPAddressResponse, 0, len(ips))
	for i := range ips {
		out = append(out, ipToWire(&ips[i]))
	}

	return out
}

func toDriverIPAddresses(reqs []wireIPAddressRequest) []driver.IPAddress {
	out := make([]driver.IPAddress, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, driver.IPAddress{SubnetID: r.SubnetID, IP: r.IP, IPv6: r.IPv6})
	}

	return out
}

func toDriverTags(tags []wireTag) []driver.Tag {
	out := make([]driver.Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, driver.Tag{Key: t.Key, Value: t.Value})
	}

	return out
}

func tagsToWire(tags []driver.Tag) []wireTag {
	out := make([]wireTag, 0, len(tags))
	for _, t := range tags {
		out = append(out, wireTag{Key: t.Key, Value: t.Value})
	}

	return out
}

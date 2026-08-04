package driver

import "context"

// Byoasn is a bring-your-own Autonomous System Number provisioned into an IPAM.
type Byoasn struct {
	Asn           string
	IpamID        string
	State         string
	StatusMessage string
}

// AsnAssociation links a BYOASN to a BYOIP CIDR.
type AsnAssociation struct {
	Asn           string
	CIDR          string
	State         string
	StatusMessage string
}

// ByoipCidr is a bring-your-own public IP CIDR (optionally moved into IPAM).
type ByoipCidr struct {
	CIDR               string
	Description        string
	State              string
	StatusMessage      string
	NetworkBorderGroup string
	AdvertisementType  string
	AsnAssociations    []AsnAssociation
}

// IPAMByoasn is an OPTIONAL AWS capability for bring-your-own ASN.
type IPAMByoasn interface {
	ProvisionIpamByoasn(ctx context.Context, ipamID, asn string) (*Byoasn, error)
	DeprovisionIpamByoasn(ctx context.Context, ipamID, asn string) (*Byoasn, error)
	DescribeIpamByoasn(ctx context.Context) ([]Byoasn, error)
	AssociateIpamByoasn(ctx context.Context, asn, cidr string) (*AsnAssociation, error)
	DisassociateIpamByoasn(ctx context.Context, asn, cidr string) (*AsnAssociation, error)
}

// IPAMByoip is an OPTIONAL AWS capability for bring-your-own public IP CIDRs
// and moving them into IPAM (public-IP insights).
type IPAMByoip interface {
	MoveByoipCidrToIpam(ctx context.Context, cidr, ipamPoolID string) (*ByoipCidr, error)
	ProvisionByoipCidr(ctx context.Context, cidr, description string) (*ByoipCidr, error)
	DeprovisionByoipCidr(ctx context.Context, cidr string) (*ByoipCidr, error)
	DescribeByoipCidrs(ctx context.Context) ([]ByoipCidr, error)
	AdvertiseByoipCidr(ctx context.Context, cidr string) (*ByoipCidr, error)
	WithdrawByoipCidr(ctx context.Context, cidr string) (*ByoipCidr, error)
}

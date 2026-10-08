package driver

import "context"

// Custom domain association statuses. The real service returns them in lower case
// (Terraform's waiter matches those exact strings), unlike most other statuses. A new association waits for the customer to
// add the DNS records, so it ends in PENDING_CERTIFICATE_DNS_VALIDATION: the
// emulator performs no DNS validation and never reaches ACTIVE by itself.
const (
	DomainStatusCreating                      = "creating"
	DomainStatusPendingCertificateDNSValidate = "pending_certificate_dns_validation"
	DomainStatusDeleting                      = "deleting"
)

// CertificateValidationRecord is a CNAME record the customer adds to validate a
// custom domain.
type CertificateValidationRecord struct {
	Name   string
	Type   string
	Value  string
	Status string
}

// CustomDomain is a custom domain associated with a service.
type CustomDomain struct {
	DomainName                   string
	EnableWWWSubdomain           bool
	Status                       string
	CertificateValidationRecords []CertificateValidationRecord
}

// VpcDNSTarget maps a VPC ingress connection's domain to the VPC it serves.
type VpcDNSTarget struct {
	DomainName              string
	VpcID                   string
	VpcIngressConnectionArn string
}

// CustomDomainResult is the AssociateCustomDomain / DisassociateCustomDomain /
// DescribeCustomDomains result.
type CustomDomainResult struct {
	ServiceArn    string
	DNSTarget     string
	CustomDomain  *CustomDomain
	CustomDomains []CustomDomain
	VpcDNSTargets []VpcDNSTarget
}

// CustomDomains is the optional custom domain capability.
type CustomDomains interface {
	AssociateCustomDomain(ctx context.Context, serviceArn, domainName string, enableWWW *bool) (*CustomDomainResult, error)
	DisassociateCustomDomain(ctx context.Context, serviceArn, domainName string) (*CustomDomainResult, error)
	DescribeCustomDomains(ctx context.Context, serviceArn string, page Page) (res *CustomDomainResult, nextToken string, err error)
}

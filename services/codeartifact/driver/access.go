package driver

import (
	"context"
	"time"
)

// AuthorizationToken is the bearer token package managers present to a
// repository endpoint, with the time it stops being valid.
type AuthorizationToken struct {
	Token      string
	Expiration time.Time
}

// Endpoint types GetRepositoryEndpoint accepts besides the default.
const (
	EndpointTypeIPv4      = "ipv4"
	EndpointTypeDualstack = "dualstack"
)

// Access is the optional capability behind `aws codeartifact login`.
type Access interface {
	// GetAuthorizationToken issues a bearer token for a domain; a zero durationSeconds means the default lifetime.
	GetAuthorizationToken(ctx context.Context, ref DomainRef, durationSeconds int64) (*AuthorizationToken, error)
	// GetRepositoryEndpoint returns the URL of a repository for one package format ("", "ipv4" or "dualstack" endpoint type).
	GetRepositoryEndpoint(ctx context.Context, ref RepositoryRef, format, endpointType string) (string, error)
}

// RepositoryAdministratorLister is the optional capability behind the administrator-account filter of
// ListRepositoriesInDomain, so the base interface keeps its signature.
type RepositoryAdministratorLister interface {
	// ListRepositoriesInDomainByAdministrator lists a domain's repositories administered by one account.
	ListRepositoriesInDomainByAdministrator(ctx context.Context, ref DomainRef, administratorAccount, prefix string,
		page Page) (repos []*Repository, nextToken string, err error)
}

package codeartifact_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func TestGetAuthorizationTokenDefaults(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	m := newMockWithOptions(config.NewOptions(config.WithClock(clk)))
	mustDomain(t, m, "dom")

	a, err := m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "dom"}, 0)
	requireNoError(t, err)

	b, err := m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "dom"}, 0)
	requireNoError(t, err)

	if a.Token == "" || a.Token == b.Token {
		t.Fatalf("tokens must be non-empty and unique: %q %q", a.Token, b.Token)
	}

	if want := clk.Now().Add(12 * time.Hour); !a.Expiration.Equal(want) {
		t.Fatalf("expiration = %v, want %v", a.Expiration, want)
	}
}

func TestGetAuthorizationTokenDurationBounds(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	m := newMockWithOptions(config.NewOptions(config.WithClock(clk)))
	mustDomain(t, m, "dom")

	for _, bad := range []int64{1, 899, 43201, -1} {
		_, err := m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "dom"}, bad)
		requireAPIError(t, err, driver.ExValidation)
	}

	for _, ok := range []int64{900, 3600, 43200} {
		tok, err := m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "dom"}, ok)
		requireNoError(t, err)

		if want := clk.Now().Add(time.Duration(ok) * time.Second); !tok.Expiration.Equal(want) {
			t.Fatalf("duration %d: expiration = %v, want %v", ok, tok.Expiration, want)
		}
	}
}

func TestGetAuthorizationTokenUnknownDomainAndOwner(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")

	_, err := m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "ghost"}, 0)
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "dom", Owner: "999999999999"}, 0)
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.GetAuthorizationToken(context.Background(), driver.DomainRef{Domain: "Bad Name"}, 0)
	requireAPIError(t, err, driver.ExValidation)
}

func TestGetRepositoryEndpointAllFormats(t *testing.T) {
	opts := config.NewOptions()
	m := newMockWithOptions(opts)
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")

	for _, format := range []string{"npm", "pypi", "maven", "nuget", "generic", "ruby", "swift", "cargo"} {
		for _, et := range []string{"", "ipv4", "dualstack"} {
			got, err := m.GetRepositoryEndpoint(context.Background(), driver.RepositoryRef{Domain: "dom", Repository: "repo"}, format, et)
			requireNoError(t, err)

			want := "https://dom-" + opts.AccountID + ".d.codeartifact." + opts.Region + ".amazonaws.com/" + format + "/repo/"
			if got != want {
				t.Fatalf("%s/%s: endpoint = %q, want %q", format, et, got, want)
			}
		}
	}
}

func TestGetRepositoryEndpointErrors(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	mustRepo(t, m, "dom", "repo")

	ref := driver.RepositoryRef{Domain: "dom", Repository: "repo"}

	_, err := m.GetRepositoryEndpoint(context.Background(), ref, "docker", "")
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.GetRepositoryEndpoint(context.Background(), ref, "npm", "ipv6")
	requireAPIError(t, err, driver.ExValidation)

	_, err = m.GetRepositoryEndpoint(context.Background(), driver.RepositoryRef{Domain: "dom", Repository: "ghost"}, "npm", "")
	requireAPIError(t, err, driver.ExNotFound)
}

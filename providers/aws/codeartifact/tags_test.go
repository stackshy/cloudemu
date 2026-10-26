package codeartifact_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func tagSet(n, offset int) map[string]string {
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		out[fmt.Sprintf("k%03d", i+offset)] = "v"
	}

	return out
}

func TestTagLimitBoundary(t *testing.T) {
	m := newMock()
	d := mustDomain(t, m, "dom")
	ctx := context.Background()

	const limit = 200

	requireNoError(t, m.TagResource(ctx, d.Arn, tagSet(limit, 0)))

	// Overwriting existing keys at the limit adds no new keys.
	requireNoError(t, m.TagResource(ctx, d.Arn, map[string]string{"k000": "changed"}))

	err := m.TagResource(ctx, d.Arn, map[string]string{"extra": "v"})
	requireAPIError(t, err, driver.ExServiceQuota)

	requireNoError(t, m.UntagResource(ctx, d.Arn, []string{"k001"}))
	requireNoError(t, m.TagResource(ctx, d.Arn, map[string]string{"extra": "v"}))

	tags, err := m.ListTagsForResource(ctx, d.Arn)
	requireNoError(t, err)

	if len(tags) != limit || tags["k000"] != "changed" {
		t.Fatalf("tags = %d (k000=%q)", len(tags), tags["k000"])
	}
}

func TestCreateWithTooManyTagsRejected(t *testing.T) {
	m := newMock()

	_, err := m.CreateDomain(context.Background(), &driver.CreateDomainInput{Name: "dom", Tags: tagSet(201, 0)})
	requireAPIError(t, err, driver.ExValidation)
}

func TestTagMalformedAndUnknownARN(t *testing.T) {
	m := newMock()
	mustDomain(t, m, "dom")
	ctx := context.Background()

	err := m.TagResource(ctx, "not-an-arn", map[string]string{"a": "b"})
	requireAPIError(t, err, driver.ExValidation)

	err = m.TagResource(ctx, "arn:aws:codeartifact:us-east-1:123456789012:domain/ghost", map[string]string{"a": "b"})
	requireAPIError(t, err, driver.ExNotFound)

	_, err = m.ListTagsForResource(ctx, "arn:aws:codeartifact:us-east-1:123456789012:repository/dom/ghost")
	requireAPIError(t, err, driver.ExNotFound)
}

func TestTagARNInAnotherAccountOrRegionIsNotFound(t *testing.T) {
	opts := config.NewOptions()
	m := newMockWithOptions(opts)
	d := mustDomain(t, m, "dom")
	repo := mustRepo(t, m, "dom", "repo")
	group := createGroup(t, m, "/npm/*")
	ctx := context.Background()

	for _, arn := range []string{d.Arn, repo.Arn, group.Arn} {
		foreignAccount := strings.Replace(arn, ":"+opts.AccountID+":", ":999999999999:", 1)
		foreignRegion := strings.Replace(arn, ":"+opts.Region+":", ":ap-south-1:", 1)

		for _, bad := range []string{foreignAccount, foreignRegion, arn + "/extra"} {
			requireAPIError(t, m.TagResource(ctx, bad, map[string]string{"a": "b"}), driver.ExNotFound)

			_, err := m.ListTagsForResource(ctx, bad)
			requireAPIError(t, err, driver.ExNotFound)
		}

		requireNoError(t, m.TagResource(ctx, arn, map[string]string{"a": "b"}))
	}
}

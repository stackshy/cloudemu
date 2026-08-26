package sesv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsses "github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// TestSDKListEmailIdentitiesPagination walks ListEmailIdentities across pages
// over three identities: PageSize=2 yields a full page with a token then a final
// page without one, each identity once.
func TestSDKListEmailIdentitiesPagination(t *testing.T) {
	c := newSESClient(t)
	ctx := context.Background()

	for _, id := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		if _, err := c.CreateEmailIdentity(ctx, &awsses.CreateEmailIdentityInput{
			EmailIdentity: aws.String(id),
		}); err != nil {
			t.Fatalf("CreateEmailIdentity(%s): %v", id, err)
		}
	}

	page1, err := c.ListEmailIdentities(ctx, &awsses.ListEmailIdentitiesInput{PageSize: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListEmailIdentities page1: %v", err)
	}

	if len(page1.EmailIdentities) != 2 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d identities token=%q, want 2 with token",
			len(page1.EmailIdentities), aws.ToString(page1.NextToken))
	}

	page2, err := c.ListEmailIdentities(ctx, &awsses.ListEmailIdentitiesInput{
		PageSize: aws.Int32(2), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListEmailIdentities page2: %v", err)
	}

	if len(page2.EmailIdentities) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d identities token=%q, want 1 no token",
			len(page2.EmailIdentities), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, id := range append(page1.EmailIdentities, page2.EmailIdentities...) {
		name := aws.ToString(id.IdentityName)
		if seen[name] {
			t.Fatalf("identity %q returned twice across pages", name)
		}

		seen[name] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique identities, want 3", len(seen))
	}

	all, err := c.ListEmailIdentities(ctx, &awsses.ListEmailIdentitiesInput{})
	if err != nil {
		t.Fatalf("ListEmailIdentities all: %v", err)
	}

	if len(all.EmailIdentities) != 3 || aws.ToString(all.NextToken) != "" {
		t.Fatalf("single page = %d identities token=%q, want 3 no token",
			len(all.EmailIdentities), aws.ToString(all.NextToken))
	}
}

// TestSDKListConfigurationSetsPagination walks ListConfigurationSets across pages
// over three configuration sets.
func TestSDKListConfigurationSetsPagination(t *testing.T) {
	c := newSESClient(t)
	ctx := context.Background()

	for _, name := range []string{"cs1", "cs2", "cs3"} {
		if _, err := c.CreateConfigurationSet(ctx, &awsses.CreateConfigurationSetInput{
			ConfigurationSetName: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateConfigurationSet(%s): %v", name, err)
		}
	}

	page1, err := c.ListConfigurationSets(ctx, &awsses.ListConfigurationSetsInput{PageSize: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListConfigurationSets page1: %v", err)
	}

	if len(page1.ConfigurationSets) != 2 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d sets token=%q, want 2 with token",
			len(page1.ConfigurationSets), aws.ToString(page1.NextToken))
	}

	page2, err := c.ListConfigurationSets(ctx, &awsses.ListConfigurationSetsInput{
		PageSize: aws.Int32(2), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListConfigurationSets page2: %v", err)
	}

	if len(page2.ConfigurationSets) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d sets token=%q, want 1 no token",
			len(page2.ConfigurationSets), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, name := range append(page1.ConfigurationSets, page2.ConfigurationSets...) {
		if seen[name] {
			t.Fatalf("config set %q returned twice across pages", name)
		}

		seen[name] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique config sets, want 3", len(seen))
	}

	all, err := c.ListConfigurationSets(ctx, &awsses.ListConfigurationSetsInput{})
	if err != nil {
		t.Fatalf("ListConfigurationSets all: %v", err)
	}

	if len(all.ConfigurationSets) != 3 || aws.ToString(all.NextToken) != "" {
		t.Fatalf("single page = %d sets token=%q, want 3 no token",
			len(all.ConfigurationSets), aws.ToString(all.NextToken))
	}
}

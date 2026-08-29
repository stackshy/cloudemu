package main

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// TestEnforceAuthRejectsInvalidSignature proves --enforce-auth threads through to
// serverkit's WithEnforceAuth: with it on, an AWS request signed with dummy
// credentials (an unregistered access key) is rejected with a 403, and with it
// off the same dummy credentials pass. The SDK signs with the static creds set in
// awsSDKConfig ("test"/"test"), which are not a registered IAM access key.
func TestEnforceAuthRejectsInvalidSignature(t *testing.T) {
	t.Run("enforced rejects", func(t *testing.T) {
		cfg := testConfig(t, allEnginesOff())
		cfg.enforceAuth = true

		awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
		defer stop()

		client := rdsClient(t, awsURL)

		_, err := client.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{})
		if err == nil {
			t.Fatalf("DescribeDBInstances with unregistered creds succeeded, want 403 under --enforce-auth")
		}

		if !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "InvalidClientTokenId") {
			t.Fatalf("error does not look like an auth rejection: %v", err)
		}
	})

	t.Run("not enforced passes", func(t *testing.T) {
		cfg := testConfig(t, allEnginesOff())
		cfg.enforceAuth = false

		awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
		defer stop()

		client := rdsClient(t, awsURL)

		if _, err := client.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{}); err != nil {
			t.Fatalf("DescribeDBInstances with dummy creds failed while --enforce-auth off: %v", err)
		}
	})
}

package main

import (
	"context"
	"io"
	"net/http"
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
		cfg.EnforceAuth = true

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

	t.Run("public operations skip the gate", func(t *testing.T) {
		cfg := testConfig(t, allEnginesOff())
		cfg.EnforceAuth = true

		awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
		defer stop()

		cases := []struct {
			name, target, host, path string
			wantGate                 bool
		}{
			{name: "InitiateAuth", target: "AWSCognitoIdentityProviderService.InitiateAuth", path: "/"},
			{name: "CreateUserPool", target: "AWSCognitoIdentityProviderService.CreateUserPool", path: "/", wantGate: true},
			{name: "execute-api", host: "abc123.execute-api.us-east-1.amazonaws.com", path: "/prod/pets"},
		}

		for _, tc := range cases {
			status, body := unsignedPost(t, awsURL+tc.path, tc.host, tc.target)
			gated := status == http.StatusForbidden && strings.Contains(body, "MissingAuthenticationToken")

			if gated != tc.wantGate {
				t.Fatalf("%s: status %d body %s, gate rejection = %v, want %v", tc.name, status, body, gated, tc.wantGate)
			}
		}
	})

	t.Run("not enforced passes", func(t *testing.T) {
		cfg := testConfig(t, allEnginesOff())
		cfg.EnforceAuth = false

		awsURL, stop := startAWS(t, cfg, mustOptions(t, &cfg))
		defer stop()

		client := rdsClient(t, awsURL)

		if _, err := client.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{}); err != nil {
			t.Fatalf("DescribeDBInstances with dummy creds failed while --enforce-auth off: %v", err)
		}
	})
}

// unsignedPost sends a POST with no SigV4 Authorization header, optionally
// overriding Host and setting X-Amz-Target, and returns the status and body.
func unsignedPost(t *testing.T, url, host, target string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if host != "" {
		req.Host = host
	}

	if target != "" {
		req.Header.Set("X-Amz-Target", target)
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

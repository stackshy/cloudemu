package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// signedJSONTarget sends a SigV4-signed AWS JSON 1.0 request for the given
// X-Amz-Target and returns the status and body.
func signedJSONTarget(t *testing.T, endpoint string, c aws.Credentials, service, target, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(context.Background(), c, req, hex.EncodeToString(sum[:]), service, "us-east-1", time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw)
}

// TestEnforceAuthNetworkFirewall proves the operations added for the Network
// Firewall parity work are authorised per IAM action under --enforce-auth: a
// user allowed only network-firewall:DescribeFirewall passes the gate on that
// operation (the handler then answers its own 400 for the unknown firewall) and
// is refused on the new ones, while an administrator is allowed everywhere.
func TestEnforceAuthNetworkFirewall(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))
	reader := boot.newUser(t, "fw-reader", allowDoc("network-firewall:DescribeFirewall"))
	const prefix = "NetworkFirewall_20201112."

	for _, op := range []string{
		"UpdateFirewallDescription", "UpdateSubnetChangeProtection", "CreateTLSInspectionConfiguration",
		"CreateVpcEndpointAssociation", "PutResourcePolicy", "StartFlowCapture", "StartAnalysisReport",
	} {
		status, body := signedJSONTarget(t, endpoint, reader, "network-firewall", prefix+op, `{}`)
		if status != http.StatusForbidden || !strings.Contains(body, "AccessDenied") {
			t.Fatalf("%s as a describe-only user: %d %s, want 403 AccessDenied", op, status, body)
		}

		status, body = signedJSONTarget(t, endpoint, boot.cred, "network-firewall", prefix+op, `{}`)
		if status == http.StatusForbidden {
			t.Fatalf("%s as the administrator: %d %s, want the handler's own answer", op, status, body)
		}
	}

	status, body := signedJSONTarget(t, endpoint, reader, "network-firewall", prefix+"DescribeFirewall", `{"FirewallName":"ghost"}`)
	if status == http.StatusForbidden || !strings.Contains(body, "ResourceNotFoundException") {
		t.Fatalf("DescribeFirewall as the allowed user: %d %s, want the handler's ResourceNotFoundException", status, body)
	}
}

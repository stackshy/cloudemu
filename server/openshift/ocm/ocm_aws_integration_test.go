package ocm_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	ocmprovider "github.com/stackshy/cloudemu/v2/providers/openshift/ocm"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	ocmserver "github.com/stackshy/cloudemu/v2/server/openshift/ocm"
)

// TestOCM_TokenThroughAWSServer verifies the OCM SSO token endpoint is reachable
// through the FULL AWS server with a form-encoded body — the request shape
// `rosa login` sends. OCM registers ahead of the AWS Query handlers, several of
// which claim any form-encoded POST and would otherwise answer the token request
// with InvalidAction. This guards that registration ordering (found via a live
// `rosa` drive, not the isolated handler test).
func TestOCM_TokenThroughAWSServer(t *testing.T) {
	cloud := cloudemu.NewAWS(config.WithAccountID("000000000000"))
	d := awsserver.DriversFrom(cloud)
	d.OCM = ocmserver.New(ocmprovider.New(config.NewOptions()))

	ts := httptest.NewServer(awsserver.New(d))
	t.Cleanup(ts.Close)

	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {"cloud-services"}}
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/auth/realms/redhat-external/protocol/openid-connect/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}

	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token via AWS server: status %d, want 200 (OCM intercepted by an AWS Query handler?)\n%s",
			resp.StatusCode, body)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}

	if err := json.Unmarshal(body, &tok); err != nil {
		t.Fatalf("decode token: %v\n%s", err, body)
	}

	if tok.AccessToken == "" || tok.TokenType != "Bearer" {
		t.Fatalf("unexpected token response (AWS handler intercepted?): %s", body)
	}
}

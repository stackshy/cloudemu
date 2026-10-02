package appsync_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

func TestLambdaAuthorizerRules(t *testing.T) {
	oneLambda := `{"authenticationType":"AWS_LAMBDA","lambdaAuthorizerConfig":` + lambdaAuth + `}`

	cases := []struct {
		name    string
		auth    string
		extra   map[string]json.RawMessage
		wantErr string
	}{
		{"ttl 3600 ok", driver.AuthLambda,
			raw("lambdaAuthorizerConfig", `{"authorizerUri":"u","authorizerResultTtlInSeconds":3600}`), ""},
		{"ttl over 3600", driver.AuthLambda,
			raw("lambdaAuthorizerConfig", `{"authorizerUri":"u","authorizerResultTtlInSeconds":3601}`),
			"1 validation error detected: Value '3601' at 'lambdaAuthorizerConfig.authorizerResultTtlInSeconds' " +
				"failed to satisfy constraint: Member must have value between 0 and 3600"},
		{"ttl negative in additional provider", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders",
				`[{"authenticationType":"AWS_LAMBDA","lambdaAuthorizerConfig":{"authorizerUri":"u","authorizerResultTtlInSeconds":-1}}]`),
			"1 validation error detected: Value '-1' at 'lambdaAuthorizerConfig.authorizerResultTtlInSeconds' " +
				"failed to satisfy constraint: Member must have value between 0 and 3600"},
		{"one additional lambda ok", driver.AuthAPIKey, raw("additionalAuthenticationProviders", `[`+oneLambda+`]`), ""},
		{"primary and additional lambda", driver.AuthLambda,
			raw("lambdaAuthorizerConfig", lambdaAuth, "additionalAuthenticationProviders", `[`+oneLambda+`]`),
			"Only one AWS_LAMBDA authorization type is allowed per API."},
		{"two additional lambdas", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders", `[`+oneLambda+`,`+oneLambda+`]`),
			"Only one AWS_LAMBDA authorization type is allowed per API."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)

			_, err := m.CreateGraphqlAPI(context.Background(), &driver.CreateGraphqlAPIInput{
				Name: "api", AuthenticationType: tc.auth, Extra: tc.extra,
			})
			if tc.wantErr != "" {
				assertBadRequest(t, err, tc.wantErr)
			} else if err != nil {
				t.Fatalf("CreateGraphqlAPI: %v", err)
			}
		})
	}
}

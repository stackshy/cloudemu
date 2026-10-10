package lambda

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		method, target, host string
		op                   opID
		route                route
		name, qualifier      string
		item                 string
	}{
		{method: "GET", target: "/2015-03-31/functions", op: opListFunctions},
		{method: "POST", target: "/2015-03-31/functions/", op: opCreateFunction},
		{method: "PUT", target: "/2015-03-31/functions", op: opUnknown},
		{method: "GET", target: "/2015-03-31/functions/f", op: opGetFunction, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f?Qualifier=1", op: opGetFunction, name: "f", qualifier: "1"},
		{method: "DELETE", target: "/2015-03-31/functions/f:prod", op: opDeleteFunction, name: "f", qualifier: "prod"},
		{
			method: "POST", target: "/2015-03-31/functions/arn:aws:lambda:eu-west-1:999999999999:function:f:prod/invocations",
			op: opInvoke, name: "f", qualifier: "prod",
		},
		{method: "POST", target: "/2015-03-31/functions/f:a/invocations?Qualifier=b", op: opUnknown, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f/invocations", op: opUnknown, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f/configuration", op: opGetFunctionConfiguration, name: "f"},
		{method: "PUT", target: "/2015-03-31/functions/f/configuration", op: opUpdateFunctionConfiguration, name: "f"},
		{method: "PUT", target: "/2015-03-31/functions/f/code", op: opUpdateFunctionCode, name: "f"},
		{method: "POST", target: "/2015-03-31/functions/f/versions", op: opPublishVersion, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f/versions", op: opListVersionsByFunction, name: "f"},
		{method: "POST", target: "/2015-03-31/functions/f/aliases", op: opCreateAlias, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f/aliases", op: opListAliases, name: "f"},
		{method: "PUT", target: "/2015-03-31/functions/f/aliases/prod", op: opUpdateAlias, name: "f", item: "prod"},
		{method: "POST", target: "/2015-03-31/functions/f/policy", op: opAddPermission, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f/policy", op: opGetPolicy, name: "f"},
		{method: "DELETE", target: "/2015-03-31/functions/f/policy/s1", op: opRemovePermission, name: "f", item: "s1"},
		{method: "GET", target: "/2015-03-31/functions/f/policy/s1", op: opUnknown, name: "f", item: "s1"},
		{method: "GET", target: "/2015-03-31/functions/f/other", op: opUnknown, name: "f"},
		{method: "GET", target: "/2015-03-31/functions/f/a/b/c", op: opUnknown, name: "f"},
		{method: "GET", target: "/2015-03-31/functionsX", op: opGetFunction, name: "X"},

		{method: "POST", target: "/anything", host: "abc.lambda-url.us-east-1.on.aws", op: opInvokeFunctionURL, route: routeFunctionURLInvoke},
		{
			method: "DELETE", target: "/2015-03-31/functions/f", host: "abc.lambda-url.us-east-1.on.aws:4566",
			op: opInvokeFunctionURL, route: routeFunctionURLInvoke,
		},

		{method: "POST", target: "/2017-03-31/tags/arn:aws:lambda:us-east-1:1:function:f", op: opTagResource, route: routeTags, name: "f", item: "arn:aws:lambda:us-east-1:1:function:f"},
		{method: "DELETE", target: "/2017-03-31/tags/x?tagKeys=a", op: opUntagResource, route: routeTags, name: "x", item: "x"},
		{method: "PUT", target: "/2017-03-31/tags/x", op: opUnknown, route: routeTags, name: "x", item: "x"},

		{method: "POST", target: "/2015-03-31/event-source-mappings/", op: opCreateEventSourceMapping, route: routeEventSourceMappings},
		{method: "GET", target: "/2015-03-31/event-source-mappings", op: opListEventSourceMappings, route: routeEventSourceMappings},
		{method: "PUT", target: "/2015-03-31/event-source-mappings/u1", op: opUpdateEventSourceMapping, route: routeEventSourceMappings, item: "u1"},
		{method: "POST", target: "/2015-03-31/event-source-mappings/u1", op: opUnknown, route: routeEventSourceMappings, item: "u1"},

		{method: "GET", target: "/2018-10-31/layers", op: opListLayers, route: routeLayers},
		{method: "GET", target: "/2018-10-31/layers?find=LayerVersion&Arn=x", op: opGetLayerVersionByArn, route: routeLayers},
		{method: "POST", target: "/2018-10-31/layers", op: opUnknown, route: routeLayers},
		{method: "POST", target: "/2018-10-31/layers/l/versions", op: opPublishLayerVersion, route: routeLayers, name: "l"},
		{method: "GET", target: "/2018-10-31/layers/l/versions", op: opListLayerVersions, route: routeLayers, name: "l"},
		{method: "DELETE", target: "/2018-10-31/layers/l/versions/2", op: opDeleteLayerVersion, route: routeLayers, name: "l", item: "2"},
		{method: "POST", target: "/2018-10-31/layers/l/versions/2/policy", op: opAddLayerVersionPermission, route: routeLayers, name: "l", item: "2"},
		{
			method: "DELETE", target: "/2018-10-31/layers/l/versions/2/policy/s",
			op: opRemoveLayerVersionPermission, route: routeLayers, name: "l", item: "2",
		},
		{method: "GET", target: "/2018-10-31/layers/l/other", op: opUnknown, route: routeLayers},

		{method: "POST", target: "/2021-10-31/functions/f/url?Qualifier=prod", op: opCreateFunctionURLConfig, route: routeFunctionURL, name: "f", qualifier: "prod"},
		{method: "GET", target: "/2021-10-31/functions/f/urls", op: opListFunctionURLConfigs, route: routeFunctionURL, name: "f"},
		{method: "GET", target: "/2021-10-31/functions/f", op: opUnknown, route: routeFunctionURL},

		{method: "PUT", target: "/2019-09-25/functions/f/event-invoke-config", op: opPutEventInvokeConfig, route: routeEventInvokeConfig, name: "f"},
		{method: "POST", target: "/2019-09-25/functions/f/event-invoke-config", op: opUpdateEventInvokeConfig, route: routeEventInvokeConfig, name: "f"},
		{method: "GET", target: "/2019-09-25/functions/f/event-invoke-config/list", op: opListEventInvokeConfigs, route: routeEventInvokeConfig, name: "f"},

		{
			method: "PUT", target: "/2019-09-30/functions/f/provisioned-concurrency?Qualifier=1",
			op: opPutProvisionedConcurrency, route: routeProvisionedConcurrency, name: "f", qualifier: "1",
		},
		{
			method: "GET", target: "/2019-09-30/functions/f/provisioned-concurrency?List=ALL",
			op: opListProvisionedConcurrency, route: routeProvisionedConcurrency, name: "f",
		},

		{method: "GET", target: "/2020-06-30/functions/f/code-signing-config", op: opGetFunctionCodeSigningConfig, route: routeCodeSigning, name: "f"},
		{method: "PUT", target: "/2020-06-30/functions/f/code-signing-config", op: opUnknown, route: routeCodeSigning, name: "f"},

		{method: "PUT", target: "/2017-10-31/functions/f/concurrency", op: opPutFunctionConcurrency, route: routeConcurrency, name: "f"},
		{method: "GET", target: "/2017-10-31/functions/f/concurrency", op: opGetFunctionConcurrency, route: routeConcurrency, name: "f"},
		{method: "DELETE", target: "/2019-09-30/functions/f/concurrency", op: opDeleteFunctionConcurrency, route: routeConcurrency, name: "f"},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, http.NoBody)
		if tc.host != "" {
			r.Host = tc.host
		}

		before := r.URL.String()
		op, a := classify(r)

		if op != tc.op || a.route != tc.route || a.name != tc.name || a.qualifier != tc.qualifier || a.item != tc.item {
			t.Errorf("%s %s: got op=%q route=%d name=%q qualifier=%q item=%q, want op=%q route=%d name=%q qualifier=%q item=%q",
				tc.method, tc.target, op, a.route, a.name, a.qualifier, a.item, tc.op, tc.route, tc.name, tc.qualifier, tc.item)
		}

		if r.URL.String() != before {
			t.Errorf("%s %s: classify changed the URL to %s", tc.method, tc.target, r.URL)
		}
	}
}

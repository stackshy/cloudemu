package eventgrid

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
)

func TestSubscriptionFilterMatches(t *testing.T) {
	tests := []struct {
		name     string
		rawProps string
		event    driver.Event
		want     bool
	}{
		{
			name:     "no filter matches everything",
			rawProps: "",
			event:    driver.Event{Subject: "anything"},
			want:     true,
		},
		{
			name:     "subjectBeginsWith matches",
			rawProps: `{"filter":{"subjectBeginsWith":"orders/"}}`,
			event:    driver.Event{Subject: "orders/123"},
			want:     true,
		},
		{
			name:     "subjectBeginsWith rejects other prefix",
			rawProps: `{"filter":{"subjectBeginsWith":"orders/"}}`,
			event:    driver.Event{Subject: "invoices/123"},
			want:     false,
		},
		{
			name:     "subjectEndsWith matches",
			rawProps: `{"filter":{"subjectEndsWith":".jpg"}}`,
			event:    driver.Event{Subject: "container/photo.jpg"},
			want:     true,
		},
		{
			name:     "subjectEndsWith rejects other suffix",
			rawProps: `{"filter":{"subjectEndsWith":".jpg"}}`,
			event:    driver.Event{Subject: "container/photo.png"},
			want:     false,
		},
		{
			name:     "case sensitivity off (default) ignores case",
			rawProps: `{"filter":{"subjectBeginsWith":"ORDERS/"}}`,
			event:    driver.Event{Subject: "orders/123"},
			want:     true,
		},
		{
			name:     "case sensitivity on enforces case",
			rawProps: `{"filter":{"subjectBeginsWith":"ORDERS/","isSubjectCaseSensitive":true}}`,
			event:    driver.Event{Subject: "orders/123"},
			want:     false,
		},
		{
			name:     "includedEventTypes matches",
			rawProps: `{"filter":{"includedEventTypes":["Order.Created","Order.Cancelled"]}}`,
			event:    driver.Event{DetailType: "Order.Created"},
			want:     true,
		},
		{
			name:     "includedEventTypes rejects unlisted type",
			rawProps: `{"filter":{"includedEventTypes":["Order.Cancelled"]}}`,
			event:    driver.Event{DetailType: "Order.Created"},
			want:     false,
		},
		{
			name: "advancedFilter StringIn on data field matches",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"StringIn","key":"data.color","values":["red","blue"]}]}}`,
			event: driver.Event{Detail: `{"color":"blue"}`},
			want:  true,
		},
		{
			name: "advancedFilter StringIn on data field rejects",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"StringIn","key":"data.color","values":["red","blue"]}]}}`,
			event: driver.Event{Detail: `{"color":"green"}`},
			want:  false,
		},
		{
			name: "advancedFilter NumberGreaterThan matches",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"NumberGreaterThan","key":"data.total","value":100}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  true,
		},
		{
			name: "advancedFilter NumberGreaterThan rejects",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"NumberGreaterThan","key":"data.total","value":500}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  false,
		},
		{
			name: "advancedFilter IsNotNull rejects missing field",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"IsNotNull","key":"data.missing"}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  false,
		},
		{
			// Per Event Grid semantics, a missing key MATCHES the negative-
			// membership operators (the field can't be in the excluded set).
			name: "advancedFilter StringNotIn matches when key is absent",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"StringNotIn","key":"data.missing","values":["a","b"]}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  true,
		},
		{
			name: "advancedFilter NumberNotIn matches when key is absent",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"NumberNotIn","key":"data.missing","values":[1,2]}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  true,
		},
		{
			// The positive-membership counterparts stay NOT-matched on a missing key.
			name: "advancedFilter StringIn rejects when key is absent",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"StringIn","key":"data.missing","values":["a","b"]}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  false,
		},
		{
			name: "advancedFilter NumberIn rejects when key is absent",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"NumberIn","key":"data.missing","values":[1,2]}]}}`,
			event: driver.Event{Detail: `{"total":250}`},
			want:  false,
		},
		{
			name: "advancedFilter StringNotIn rejects when present value is excluded",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"StringNotIn","key":"data.color","values":["red","blue"]}]}}`,
			event: driver.Event{Detail: `{"color":"red"}`},
			want:  false,
		},
		{
			name: "advancedFilter on subject field",
			rawProps: `{"filter":{"advancedFilters":[
				{"operatorType":"StringBeginsWith","key":"subject","values":["orders/"]}]}}`,
			event: driver.Event{Subject: "orders/1"},
			want:  true,
		},
		{
			name: "multiple criteria are ANDed",
			rawProps: `{"filter":{"subjectBeginsWith":"orders/","includedEventTypes":["Order.Created"],
				"advancedFilters":[{"operatorType":"NumberGreaterThan","key":"data.total","value":100}]}}`,
			event: driver.Event{Subject: "orders/1", DetailType: "Order.Created", Detail: `{"total":250}`},
			want:  true,
		},
		{
			name: "multiple criteria: one mismatch fails the AND",
			rawProps: `{"filter":{"subjectBeginsWith":"orders/","includedEventTypes":["Order.Created"],
				"advancedFilters":[{"operatorType":"NumberGreaterThan","key":"data.total","value":1000}]}}`,
			event: driver.Event{Subject: "orders/1", DetailType: "Order.Created", Detail: `{"total":250}`},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := parseSubscriptionFilter(tc.rawProps)
			assert.Equal(t, tc.want, f.matches(&tc.event))
		})
	}
}

func TestParseSubscriptionDestination(t *testing.T) {
	d := parseSubscriptionDestination(
		`{"destination":{"endpointType":"WebHook","properties":{"endpointUrl":"https://example.com/hook"}}}`)

	assert.Equal(t, endpointTypeWebHook, d.EndpointType)
	assert.Equal(t, "https://example.com/hook", d.EndpointURL)
}

func TestParseSubscriptionDestinationServiceBus(t *testing.T) {
	d := parseSubscriptionDestination(`{"destination":{"endpointType":"ServiceBusQueue","properties":{
		"resourceId":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.ServiceBus/namespaces/ns/queues/q"}}}`)

	assert.Equal(t, "ServiceBusQueue", d.EndpointType)
	assert.Equal(t, "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ServiceBus/namespaces/ns/queues/q", d.ResourceID)
}

func TestParseSubscriptionDestinationEmpty(t *testing.T) {
	d := parseSubscriptionDestination("")
	assert.Equal(t, subscriptionDestination{}, d)
}

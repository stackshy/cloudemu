package appsync_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// TestUpdateDataSourceKeepsOmittedBlock mirrors terraform-provider-aws, which
// never sends eventBridgeConfig on UpdateDataSource.
func TestUpdateDataSourceKeepsOmittedBlock(t *testing.T) {
	const bus = `{"eventBusArn":"arn:aws:events:us-east-1:123456789012:event-bus/default"}`

	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "api")

	if _, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		APIID: api.APIID, Name: "bus", Type: driver.DataSourceEventBridge, ServiceRoleArn: roleARN,
		Extra: raw("eventBridgeConfig", bus),
	}); err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	upd, err := m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{
		APIID: api.APIID, Name: "bus", Type: driver.DataSourceEventBridge, ServiceRoleArn: roleARN, Description: "changed",
	})
	if err != nil {
		t.Fatalf("UpdateDataSource without eventBridgeConfig: %v", err)
	}

	if upd.Description != "changed" || string(upd.Extra["eventBridgeConfig"]) != bus {
		t.Fatalf("stored block not kept: %#v", upd)
	}

	got, err := m.GetDataSource(ctx, api.APIID, "bus")
	if err != nil || string(got.Extra["eventBridgeConfig"]) != bus {
		t.Fatalf("GetDataSource lost the block: %v %#v", err, got)
	}

	// A block for another type is still rejected.
	_, err = m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{
		APIID: api.APIID, Name: "bus", Type: driver.DataSourceEventBridge, ServiceRoleArn: roleARN,
		Extra: raw("httpConfig", `{"endpoint":"https://example.com"}`),
	})
	assertBadRequest(t, err, "HttpConfig is not supported for data source type AMAZON_EVENTBRIDGE.")

	// Changing type with no stored block for the new type still needs one.
	_, err = m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{
		APIID: api.APIID, Name: "bus", Type: driver.DataSourceDynamoDB, ServiceRoleArn: roleARN,
	})
	assertBadRequest(t, err, "DynamodbConfig can't be null.")
}

func TestUpdateGraphqlAPIKeepsOmittedAuthBlock(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	api, err := m.CreateGraphqlAPI(ctx, &driver.CreateGraphqlAPIInput{
		Name: "api", AuthenticationType: driver.AuthCognito, Extra: raw("userPoolConfig", userPoolJSON),
	})
	if err != nil {
		t.Fatalf("CreateGraphqlAPI: %v", err)
	}

	upd, err := m.UpdateGraphqlAPI(ctx, &driver.UpdateGraphqlAPIInput{
		APIID: api.APIID, Name: "renamed", AuthenticationType: driver.AuthCognito,
	})
	if err != nil {
		t.Fatalf("UpdateGraphqlAPI without userPoolConfig: %v", err)
	}

	if string(upd.Extra["userPoolConfig"]) != userPoolJSON {
		t.Fatalf("userPoolConfig not kept: %#v", upd.Extra)
	}

	// Switching to a type whose block was never stored still needs it.
	_, err = m.UpdateGraphqlAPI(ctx, &driver.UpdateGraphqlAPIInput{
		APIID: api.APIID, Name: "renamed", AuthenticationType: driver.AuthOpenIDConnect,
	})
	assertBadRequest(t, err, "OpenIDConnectConfig can't be null.")
}

package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKListVersionsByFunctionPagination walks ListVersionsByFunction across
// pages: two publishes plus $LATEST give three versions, so MaxItems=2 yields a
// full page with a marker then a final page without one, each version once.
func TestSDKListVersionsByFunctionPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "vfn")

	for range 2 {
		if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
			FunctionName: aws.String("vfn"),
		}); err != nil {
			t.Fatalf("PublishVersion: %v", err)
		}
	}

	page1, err := client.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String("vfn"), MaxItems: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListVersionsByFunction page1: %v", err)
	}

	if len(page1.Versions) != 2 || aws.ToString(page1.NextMarker) == "" {
		t.Fatalf("page1 = %d versions marker=%q, want 2 with marker", len(page1.Versions), aws.ToString(page1.NextMarker))
	}

	page2, err := client.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String("vfn"), MaxItems: aws.Int32(2), Marker: page1.NextMarker,
	})
	if err != nil {
		t.Fatalf("ListVersionsByFunction page2: %v", err)
	}

	if len(page2.Versions) != 1 || aws.ToString(page2.NextMarker) != "" {
		t.Fatalf("page2 = %d versions marker=%q, want 1 no marker", len(page2.Versions), aws.ToString(page2.NextMarker))
	}

	seen := map[string]bool{}
	for _, v := range append(page1.Versions, page2.Versions...) {
		ver := aws.ToString(v.Version)
		if seen[ver] {
			t.Fatalf("version %q returned twice across pages", ver)
		}

		seen[ver] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique versions, want 3", len(seen))
	}

	// A single unpaged call returns every version and no marker.
	all, err := client.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String("vfn"),
	})
	if err != nil {
		t.Fatalf("ListVersionsByFunction all: %v", err)
	}

	if len(all.Versions) != 3 || aws.ToString(all.NextMarker) != "" {
		t.Fatalf("single page = %d versions marker=%q, want 3 no marker", len(all.Versions), aws.ToString(all.NextMarker))
	}
}

// TestSDKListAliasesPagination walks ListAliases across pages over three aliases.
func TestSDKListAliasesPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "afn")

	for _, name := range []string{"a1", "a2", "a3"} {
		if _, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
			FunctionName: aws.String("afn"), Name: aws.String(name), FunctionVersion: aws.String("$LATEST"),
		}); err != nil {
			t.Fatalf("CreateAlias(%s): %v", name, err)
		}
	}

	page1, err := client.ListAliases(ctx, &awslambda.ListAliasesInput{
		FunctionName: aws.String("afn"), MaxItems: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListAliases page1: %v", err)
	}

	if len(page1.Aliases) != 2 || aws.ToString(page1.NextMarker) == "" {
		t.Fatalf("page1 = %d aliases marker=%q, want 2 with marker", len(page1.Aliases), aws.ToString(page1.NextMarker))
	}

	page2, err := client.ListAliases(ctx, &awslambda.ListAliasesInput{
		FunctionName: aws.String("afn"), MaxItems: aws.Int32(2), Marker: page1.NextMarker,
	})
	if err != nil {
		t.Fatalf("ListAliases page2: %v", err)
	}

	if len(page2.Aliases) != 1 || aws.ToString(page2.NextMarker) != "" {
		t.Fatalf("page2 = %d aliases marker=%q, want 1 no marker", len(page2.Aliases), aws.ToString(page2.NextMarker))
	}

	seen := map[string]bool{}
	for _, a := range append(page1.Aliases, page2.Aliases...) {
		seen[aws.ToString(a.Name)] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique aliases, want 3", len(seen))
	}

	all, err := client.ListAliases(ctx, &awslambda.ListAliasesInput{FunctionName: aws.String("afn")})
	if err != nil {
		t.Fatalf("ListAliases all: %v", err)
	}

	if len(all.Aliases) != 3 || aws.ToString(all.NextMarker) != "" {
		t.Fatalf("single page = %d aliases marker=%q, want 3 no marker", len(all.Aliases), aws.ToString(all.NextMarker))
	}
}

// TestSDKListEventSourceMappingsPagination walks ListEventSourceMappings across
// pages over three mappings.
func TestSDKListEventSourceMappingsPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "efn")

	for _, arn := range []string{
		"arn:aws:sqs:us-east-1:000000000000:q1",
		"arn:aws:sqs:us-east-1:000000000000:q2",
		"arn:aws:sqs:us-east-1:000000000000:q3",
	} {
		if _, err := client.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
			FunctionName: aws.String("efn"), EventSourceArn: aws.String(arn),
		}); err != nil {
			t.Fatalf("CreateEventSourceMapping(%s): %v", arn, err)
		}
	}

	page1, err := client.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{
		FunctionName: aws.String("efn"), MaxItems: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListEventSourceMappings page1: %v", err)
	}

	if len(page1.EventSourceMappings) != 2 || aws.ToString(page1.NextMarker) == "" {
		t.Fatalf("page1 = %d mappings marker=%q, want 2 with marker",
			len(page1.EventSourceMappings), aws.ToString(page1.NextMarker))
	}

	page2, err := client.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{
		FunctionName: aws.String("efn"), MaxItems: aws.Int32(2), Marker: page1.NextMarker,
	})
	if err != nil {
		t.Fatalf("ListEventSourceMappings page2: %v", err)
	}

	if len(page2.EventSourceMappings) != 1 || aws.ToString(page2.NextMarker) != "" {
		t.Fatalf("page2 = %d mappings marker=%q, want 1 no marker",
			len(page2.EventSourceMappings), aws.ToString(page2.NextMarker))
	}

	seen := map[string]bool{}
	for _, m := range append(page1.EventSourceMappings, page2.EventSourceMappings...) {
		seen[aws.ToString(m.UUID)] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique mappings, want 3", len(seen))
	}

	all, err := client.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{
		FunctionName: aws.String("efn"),
	})
	if err != nil {
		t.Fatalf("ListEventSourceMappings all: %v", err)
	}

	if len(all.EventSourceMappings) != 3 || aws.ToString(all.NextMarker) != "" {
		t.Fatalf("single page = %d mappings marker=%q, want 3 no marker",
			len(all.EventSourceMappings), aws.ToString(all.NextMarker))
	}
}

// TestSDKListLayersPagination walks ListLayers across pages over three layers.
func TestSDKListLayersPagination(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	for _, name := range []string{"layer1", "layer2", "layer3"} {
		if _, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
			LayerName: aws.String(name),
			Content:   &lambdatypes.LayerVersionContentInput{ZipFile: []byte("z-" + name)},
		}); err != nil {
			t.Fatalf("PublishLayerVersion(%s): %v", name, err)
		}
	}

	page1, err := client.ListLayers(ctx, &awslambda.ListLayersInput{MaxItems: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListLayers page1: %v", err)
	}

	if len(page1.Layers) != 2 || aws.ToString(page1.NextMarker) == "" {
		t.Fatalf("page1 = %d layers marker=%q, want 2 with marker", len(page1.Layers), aws.ToString(page1.NextMarker))
	}

	page2, err := client.ListLayers(ctx, &awslambda.ListLayersInput{
		MaxItems: aws.Int32(2), Marker: page1.NextMarker,
	})
	if err != nil {
		t.Fatalf("ListLayers page2: %v", err)
	}

	if len(page2.Layers) != 1 || aws.ToString(page2.NextMarker) != "" {
		t.Fatalf("page2 = %d layers marker=%q, want 1 no marker", len(page2.Layers), aws.ToString(page2.NextMarker))
	}

	seen := map[string]bool{}
	for _, l := range append(page1.Layers, page2.Layers...) {
		seen[aws.ToString(l.LayerName)] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique layers, want 3", len(seen))
	}

	all, err := client.ListLayers(ctx, &awslambda.ListLayersInput{})
	if err != nil {
		t.Fatalf("ListLayers all: %v", err)
	}

	if len(all.Layers) != 3 || aws.ToString(all.NextMarker) != "" {
		t.Fatalf("single page = %d layers marker=%q, want 3 no marker", len(all.Layers), aws.ToString(all.NextMarker))
	}
}

package ec2_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// These tests pin that the EC2 Describe handlers that gained MaxResults/NextToken
// paging honour it the way DescribeSubnets/DescribeInstances already do: a small
// MaxResults walks the whole set exactly once via the echoed NextToken, paging
// combines with an explicit id-list (the set is filtered first, then paged), and
// an unparsable NextToken restarts from the beginning (shared pageNetworkingXML
// behaviour) rather than erroring.
const (
	pageSize      = 2
	itemCount     = 5
	filterCount   = 3
	maxAll        = 1000
	volumeSizeGiB = 10
	bogusToken    = "!!!not-a-valid-token!!!"
	nameTagKey    = "Name"
)

// pageFunc issues one Describe request: max is the MaxResults, ids restricts the
// result set (explicit id-list or resource-id filter), tok is the NextToken to
// resume from. It returns the page's stable ids plus the echoed NextToken.
type pageFunc func(max int32, ids []string, tok *string) ([]string, *string, error)

// mapIDs projects a slice of SDK shapes to their stable ids.
func mapIDs[T any](xs []T, id func(T) string) []string {
	out := make([]string, 0, len(xs))
	for i := range xs {
		out = append(out, id(xs[i]))
	}

	return out
}

// collectAll fetches the whole (optionally id-filtered) set in one request and
// asserts a within-cap MaxResults leaves no dangling NextToken.
func collectAll(t *testing.T, f pageFunc, ids []string) []string {
	t.Helper()

	got, next, err := f(maxAll, ids, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if aws.ToString(next) != "" {
		t.Fatalf("MaxResults=%d returned a NextToken, want the full set in one page", maxAll)
	}

	return got
}

// pageWalk drives f to exhaustion with MaxResults=pageSize and asserts the
// server never overfills a page, the walk terminates, and every want id is seen
// exactly once (no duplicates, gaps, or skips across the cursor).
func pageWalk(t *testing.T, want, ids []string, f pageFunc) {
	t.Helper()

	seen := map[string]int{}

	var tok *string

	for iter := 0; iter <= len(want)+1; iter++ {
		page, next, err := f(pageSize, ids, tok)
		if err != nil {
			t.Fatalf("page: %v", err)
		}

		if len(page) > pageSize {
			t.Fatalf("page returned %d items, want at most %d", len(page), pageSize)
		}

		for _, id := range page {
			seen[id]++
		}

		if aws.ToString(next) == "" {
			assertEachOnce(t, want, seen)
			return
		}

		tok = next
	}

	t.Fatal("pagination did not terminate")
}

// assertEachOnce checks the walked multiset equals want with multiplicity one.
func assertEachOnce(t *testing.T, want []string, seen map[string]int) {
	t.Helper()

	if len(seen) != len(want) {
		t.Fatalf("walked %d ids, want %d", len(seen), len(want))
	}

	for _, id := range want {
		if seen[id] != 1 {
			t.Fatalf("id %s seen %d times across pages, want exactly 1", id, seen[id])
		}
	}
}

// assertInvalidToken pins that an unparsable NextToken restarts at the start
// (matching pageNetworkingXML) rather than erroring or skipping.
func assertInvalidToken(t *testing.T, wantN int, f pageFunc) {
	t.Helper()

	got, _, err := f(maxAll, nil, aws.String(bogusToken))
	if err != nil {
		t.Fatalf("invalid NextToken errored, want a restart from the start: %v", err)
	}

	if len(got) != wantN {
		t.Fatalf("invalid NextToken returned %d items, want the full set of %d (restart at start)", len(got), wantN)
	}
}

// runPagingChecks runs the three shared assertions against one Describe handler:
// full walk, id-list-filtered walk, and invalid-token restart.
func runPagingChecks(t *testing.T, created []string, f pageFunc) {
	t.Helper()

	all := collectAll(t, f, nil)
	pageWalk(t, all, nil, f)

	sub := created[:filterCount]
	pageWalk(t, collectAll(t, f, sub), sub, f)

	assertInvalidToken(t, len(all), f)
}

func nameTagSpec(rt ec2types.ResourceType, i int) []ec2types.TagSpecification {
	return []ec2types.TagSpecification{{
		ResourceType: rt,
		Tags:         []ec2types.Tag{{Key: aws.String(nameTagKey), Value: aws.String(fmt.Sprintf("item-%d", i))}},
	}}
}

func mkVolumes(ctx context.Context, t *testing.T, c *ec2.Client, n int) []string {
	t.Helper()

	ids := make([]string, 0, n)

	for i := 0; i < n; i++ {
		out, err := c.CreateVolume(ctx, &ec2.CreateVolumeInput{
			AvailabilityZone:  aws.String("us-east-1a"),
			Size:              aws.Int32(volumeSizeGiB),
			TagSpecifications: nameTagSpec(ec2types.ResourceTypeVolume, i),
		})
		if err != nil {
			t.Fatalf("CreateVolume: %v", err)
		}

		ids = append(ids, aws.ToString(out.VolumeId))
	}

	return ids
}

func TestDescribeVolumesPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	created := mkVolumes(ctx, t, c, itemCount)

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: ids, MaxResults: aws.Int32(max), NextToken: tok})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.Volumes, func(v ec2types.Volume) string { return aws.ToString(v.VolumeId) }), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeSnapshotsPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	volID := mkVolumes(ctx, t, c, 1)[0]

	created := make([]string, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		out, err := c.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{VolumeId: aws.String(volID)})
		if err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}

		created = append(created, aws.ToString(out.SnapshotId))
	}

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{SnapshotIds: ids, MaxResults: aws.Int32(max), NextToken: tok})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.Snapshots, func(s ec2types.Snapshot) string { return aws.ToString(s.SnapshotId) }), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeImagesPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	created := make([]string, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		out, err := c.RegisterImage(ctx, &ec2.RegisterImageInput{
			Name:               aws.String(fmt.Sprintf("ami-pgn-%d", i)),
			Architecture:       ec2types.ArchitectureValuesX8664,
			RootDeviceName:     aws.String("/dev/xvda"),
			VirtualizationType: aws.String("hvm"),
		})
		if err != nil {
			t.Fatalf("RegisterImage: %v", err)
		}

		created = append(created, aws.ToString(out.ImageId))
	}

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: ids, MaxResults: aws.Int32(max), NextToken: tok})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.Images, func(im ec2types.Image) string { return aws.ToString(im.ImageId) }), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeNetworkInterfacesPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")
	subnetID := aws.ToString(mkSubnet(ctx, t, c, vpcID, "10.0.1.0/24", "us-east-1a").SubnetId)

	created := make([]string, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		out, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{SubnetId: aws.String(subnetID)})
		if err != nil {
			t.Fatalf("CreateNetworkInterface: %v", err)
		}

		created = append(created, aws.ToString(out.NetworkInterface.NetworkInterfaceId))
	}

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
			NetworkInterfaceIds: ids, MaxResults: aws.Int32(max), NextToken: tok,
		})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.NetworkInterfaces, func(n ec2types.NetworkInterface) string {
			return aws.ToString(n.NetworkInterfaceId)
		}), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeVpcEndpointsPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	vpcID := mkVPC(ctx, t, c, "10.0.0.0/16")

	created := make([]string, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		out, err := c.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
			VpcId:       aws.String(vpcID),
			ServiceName: aws.String(fmt.Sprintf("com.amazonaws.us-east-1.svc%d", i)),
		})
		if err != nil {
			t.Fatalf("CreateVpcEndpoint: %v", err)
		}

		created = append(created, aws.ToString(out.VpcEndpoint.VpcEndpointId))
	}

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{VpcEndpointIds: ids, MaxResults: aws.Int32(max), NextToken: tok})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.VpcEndpoints, func(e ec2types.VpcEndpoint) string { return aws.ToString(e.VpcEndpointId) }), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeVpcPeeringConnectionsPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	reqVPC := mkVPC(ctx, t, c, "172.16.0.0/16")

	created := make([]string, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		peerVPC := mkVPC(ctx, t, c, fmt.Sprintf("10.%d.0.0/16", i))

		out, err := c.CreateVpcPeeringConnection(ctx, &ec2.CreateVpcPeeringConnectionInput{
			VpcId: aws.String(reqVPC), PeerVpcId: aws.String(peerVPC),
		})
		if err != nil {
			t.Fatalf("CreateVpcPeeringConnection: %v", err)
		}

		created = append(created, aws.ToString(out.VpcPeeringConnection.VpcPeeringConnectionId))
	}

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeVpcPeeringConnections(ctx, &ec2.DescribeVpcPeeringConnectionsInput{
			VpcPeeringConnectionIds: ids, MaxResults: aws.Int32(max), NextToken: tok,
		})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.VpcPeeringConnections, func(p ec2types.VpcPeeringConnection) string {
			return aws.ToString(p.VpcPeeringConnectionId)
		}), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeDhcpOptionsPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	created := make([]string, 0, itemCount)
	for i := 0; i < itemCount; i++ {
		out, err := c.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
			DhcpConfigurations: []ec2types.NewDhcpConfiguration{{
				Key: aws.String("domain-name"), Values: []string{fmt.Sprintf("d%d.example", i)},
			}},
		})
		if err != nil {
			t.Fatalf("CreateDhcpOptions: %v", err)
		}

		created = append(created, aws.ToString(out.DhcpOptions.DhcpOptionsId))
	}

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		out, err := c.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{DhcpOptionsIds: ids, MaxResults: aws.Int32(max), NextToken: tok})
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.DhcpOptions, func(d ec2types.DhcpOptions) string { return aws.ToString(d.DhcpOptionsId) }), out.NextToken, nil
	}

	runPagingChecks(t, created, f)
}

func TestDescribeTagsPagination(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	// Each volume carries exactly one Name tag, so the DescribeTags tuple set is
	// one (resource-id, key) pair per volume; ids restricts via a resource-id filter.
	volIDs := mkVolumes(ctx, t, c, itemCount)

	f := func(max int32, ids []string, tok *string) ([]string, *string, error) {
		in := &ec2.DescribeTagsInput{MaxResults: aws.Int32(max), NextToken: tok}
		if len(ids) > 0 {
			in.Filters = []ec2types.Filter{{Name: aws.String("resource-id"), Values: ids}}
		}

		out, err := c.DescribeTags(ctx, in)
		if err != nil {
			return nil, nil, err
		}

		return mapIDs(out.Tags, func(td ec2types.TagDescription) string {
			return aws.ToString(td.ResourceId) + "\x00" + aws.ToString(td.Key)
		}), out.NextToken, nil
	}

	runPagingChecks(t, volIDs, f)
}

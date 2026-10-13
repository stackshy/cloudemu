package compute_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
)

// sdkConfig is an oci-go-sdk configuration provider with a throwaway key; the
// emulator does not verify request signatures.
func sdkConfig(t *testing.T) common.ConfigurationProvider {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	return common.NewRawConfigurationProvider(
		config.DefaultTenancyOCID, "ocid1.user.oc1..sdk", "us-ashburn-1", "aa:bb", string(keyPEM), nil)
}

// sdkClients are the oci-go-sdk core clients pointed at a full OCI server.
type sdkClients struct {
	compute core.ComputeClient
	storage core.BlockstorageClient
	network core.VirtualNetworkClient
}

func newSDKClients(t *testing.T) sdkClients {
	t.Helper()

	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(ociprovider.New())))
	t.Cleanup(ts.Close)

	cp := sdkConfig(t)

	compute, err := core.NewComputeClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	compute.Host = ts.URL

	storage, err := core.NewBlockstorageClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	storage.Host = ts.URL

	network, err := core.NewVirtualNetworkClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	network.Host = ts.URL

	return sdkClients{compute: compute, storage: storage, network: network}
}

func TestComputeSDKRoundTrip(t *testing.T) {
	c := newSDKClients(t)
	ctx := context.Background()
	compartment := common.String(config.DefaultTenancyOCID)

	vcn, err := c.network.CreateVcn(ctx, core.CreateVcnRequest{CreateVcnDetails: core.CreateVcnDetails{
		CompartmentId: compartment, CidrBlock: common.String("10.0.0.0/16"),
	}})
	require.NoError(t, err)

	subnet, err := c.network.CreateSubnet(ctx, core.CreateSubnetRequest{CreateSubnetDetails: core.CreateSubnetDetails{
		CompartmentId: compartment, VcnId: vcn.Id, CidrBlock: common.String("10.0.1.0/24"),
	}})
	require.NoError(t, err)

	images, err := c.compute.ListImages(ctx, core.ListImagesRequest{CompartmentId: compartment})
	require.NoError(t, err)
	require.NotEmpty(t, images.Items)

	launched, err := c.compute.LaunchInstance(ctx, core.LaunchInstanceRequest{
		LaunchInstanceDetails: core.LaunchInstanceDetails{
			CompartmentId:      compartment,
			AvailabilityDomain: common.String("cloudemu:US-ASHBURN-1-AD-1"),
			DisplayName:        common.String("sdk-web"),
			Shape:              common.String("VM.Standard.E4.Flex"),
			ShapeConfig: &core.LaunchInstanceShapeConfigDetails{
				Ocpus: common.Float32(2), MemoryInGBs: common.Float32(32),
			},
			SourceDetails:     core.InstanceSourceViaImageDetails{ImageId: images.Items[0].Id},
			CreateVnicDetails: &core.CreateVnicDetails{SubnetId: subnet.Id},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, core.InstanceLifecycleStateRunning, launched.LifecycleState)

	id := launched.Id

	got, err := c.compute.GetInstance(ctx, core.GetInstanceRequest{InstanceId: id})
	require.NoError(t, err)
	assert.Equal(t, "sdk-web", *got.DisplayName)
	assert.InDelta(t, 2.0, *got.ShapeConfig.Ocpus, 0.001)

	for _, step := range []struct {
		action core.InstanceActionActionEnum
		want   core.InstanceLifecycleStateEnum
	}{
		{core.InstanceActionActionStop, core.InstanceLifecycleStateStopped},
		{core.InstanceActionActionStart, core.InstanceLifecycleStateRunning},
	} {
		resp, err := c.compute.InstanceAction(ctx, core.InstanceActionRequest{InstanceId: id, Action: step.action})
		require.NoError(t, err)
		assert.Equal(t, step.want, resp.LifecycleState, step.action)
	}

	vol, err := c.storage.CreateVolume(ctx, core.CreateVolumeRequest{CreateVolumeDetails: core.CreateVolumeDetails{
		CompartmentId: compartment, AvailabilityDomain: got.AvailabilityDomain, SizeInGBs: common.Int64(100),
	}})
	require.NoError(t, err)

	attached, err := c.compute.AttachVolume(ctx, core.AttachVolumeRequest{
		AttachVolumeDetails: core.AttachIScsiVolumeDetails{InstanceId: id, VolumeId: vol.Id},
	})
	require.NoError(t, err)

	iscsi, ok := attached.VolumeAttachment.(core.IScsiVolumeAttachment)
	require.True(t, ok, "got %T", attached.VolumeAttachment)
	require.NotNil(t, iscsi.Ipv4)
	require.NotNil(t, iscsi.Iqn)
	require.NotNil(t, iscsi.Port)
	assert.Equal(t, 3260, *iscsi.Port)

	_, err = c.compute.DetachVolume(ctx, core.DetachVolumeRequest{VolumeAttachmentId: iscsi.Id})
	require.NoError(t, err)

	freed, err := c.storage.GetVolume(ctx, core.GetVolumeRequest{VolumeId: vol.Id})
	require.NoError(t, err)
	assert.Equal(t, core.VolumeLifecycleStateAvailable, freed.LifecycleState)

	_, err = c.compute.TerminateInstance(ctx, core.TerminateInstanceRequest{InstanceId: id})
	require.NoError(t, err)

	_, err = c.compute.GetInstance(ctx, core.GetInstanceRequest{InstanceId: id})

	svcErr, ok := common.IsServiceError(err)
	require.True(t, ok, "got %v", err)
	assert.Equal(t, 404, svcErr.GetHTTPStatusCode())
}

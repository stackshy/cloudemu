package iam_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestSDKCreateVirtualMFADevice(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateVirtualMFADevice(ctx, &iam.CreateVirtualMFADeviceInput{
		VirtualMFADeviceName: aws.String("root-mfa"),
	})
	if err != nil {
		t.Fatalf("CreateVirtualMFADevice: %v", err)
	}

	serial := aws.ToString(out.VirtualMFADevice.SerialNumber)
	if !strings.HasSuffix(serial, ":mfa/root-mfa") {
		t.Fatalf("serial number %q does not end with :mfa/root-mfa", serial)
	}

	if len(out.VirtualMFADevice.Base32StringSeed) == 0 {
		t.Fatal("Base32StringSeed is empty")
	}

	if len(out.VirtualMFADevice.QRCodePNG) == 0 {
		t.Fatal("QRCodePNG is empty")
	}
}

func TestSDKListMFADevices(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("mfa-user")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// A user with no assigned device gets an empty list (a freshly created
	// virtual device is unassigned until EnableMFADevice).
	out, err := client.ListMFADevices(ctx, &iam.ListMFADevicesInput{UserName: aws.String("mfa-user")})
	if err != nil {
		t.Fatalf("ListMFADevices: %v", err)
	}

	if len(out.MFADevices) != 0 {
		t.Fatalf("got %d MFA devices, want 0", len(out.MFADevices))
	}

	// Listing devices for an unknown user is NoSuchEntity.
	if _, err := client.ListMFADevices(ctx, &iam.ListMFADevicesInput{UserName: aws.String("ghost")}); err == nil {
		t.Fatal("ListMFADevices for unknown user: expected error, got nil")
	}
}

// TestSDKVirtualMFADeviceLifecycle drives the full real-user flow through the
// aws-sdk-go-v2 IAM client: create, enable, observe it in both ListMFADevices
// and ListVirtualMFADevices, then the delete-conflict/deactivate/delete tail.
func TestSDKVirtualMFADeviceLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("lifecycle-user")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	created, err := client.CreateVirtualMFADevice(ctx, &iam.CreateVirtualMFADeviceInput{
		VirtualMFADeviceName: aws.String("lifecycle-mfa"),
	})
	if err != nil {
		t.Fatalf("CreateVirtualMFADevice: %v", err)
	}

	serial := created.VirtualMFADevice.SerialNumber

	// A second create with the same name is EntityAlreadyExists.
	_, err = client.CreateVirtualMFADevice(ctx, &iam.CreateVirtualMFADeviceInput{
		VirtualMFADeviceName: aws.String("lifecycle-mfa"),
	})
	if err == nil {
		t.Fatal("duplicate CreateVirtualMFADevice: expected error, got nil")
	}

	var eae *types.EntityAlreadyExistsException
	if !errors.As(err, &eae) {
		t.Fatalf("duplicate create error = %T, want *EntityAlreadyExistsException: %v", err, err)
	}

	// The emulator accepts any two distinct codes.
	_, err = client.EnableMFADevice(ctx, &iam.EnableMFADeviceInput{
		UserName:            aws.String("lifecycle-user"),
		SerialNumber:        serial,
		AuthenticationCode1: aws.String("111111"),
		AuthenticationCode2: aws.String("222222"),
	})
	if err != nil {
		t.Fatalf("EnableMFADevice: %v", err)
	}

	listed, err := client.ListMFADevices(ctx, &iam.ListMFADevicesInput{UserName: aws.String("lifecycle-user")})
	if err != nil {
		t.Fatalf("ListMFADevices: %v", err)
	}

	if len(listed.MFADevices) != 1 || aws.ToString(listed.MFADevices[0].SerialNumber) != aws.ToString(serial) {
		t.Fatalf("ListMFADevices after enable = %+v, want one entry for %s", listed.MFADevices, aws.ToString(serial))
	}

	virtual, err := client.ListVirtualMFADevices(ctx, &iam.ListVirtualMFADevicesInput{
		AssignmentStatus: types.AssignmentStatusTypeAssigned,
	})
	if err != nil {
		t.Fatalf("ListVirtualMFADevices(Assigned): %v", err)
	}

	if len(virtual.VirtualMFADevices) != 1 {
		t.Fatalf("ListVirtualMFADevices(Assigned) returned %d devices, want 1", len(virtual.VirtualMFADevices))
	}

	if virtual.VirtualMFADevices[0].User == nil || aws.ToString(virtual.VirtualMFADevices[0].User.UserName) != "lifecycle-user" {
		t.Fatalf("assigned device's user = %v, want lifecycle-user", virtual.VirtualMFADevices[0].User)
	}

	// Deleting an enabled device is a DeleteConflict.
	_, err = client.DeleteVirtualMFADevice(ctx, &iam.DeleteVirtualMFADeviceInput{SerialNumber: serial})
	if err == nil {
		t.Fatal("DeleteVirtualMFADevice on an enabled device: expected error, got nil")
	}

	var dce *types.DeleteConflictException
	if !errors.As(err, &dce) {
		t.Fatalf("delete-enabled-device error = %T, want *DeleteConflictException: %v", err, err)
	}

	// Deactivate, then the delete succeeds.
	if _, err := client.DeactivateMFADevice(ctx, &iam.DeactivateMFADeviceInput{
		UserName:     aws.String("lifecycle-user"),
		SerialNumber: serial,
	}); err != nil {
		t.Fatalf("DeactivateMFADevice: %v", err)
	}

	listed, err = client.ListMFADevices(ctx, &iam.ListMFADevicesInput{UserName: aws.String("lifecycle-user")})
	if err != nil {
		t.Fatalf("ListMFADevices after deactivate: %v", err)
	}

	if len(listed.MFADevices) != 0 {
		t.Fatalf("ListMFADevices after deactivate = %+v, want empty", listed.MFADevices)
	}

	if _, err := client.DeleteVirtualMFADevice(ctx, &iam.DeleteVirtualMFADeviceInput{SerialNumber: serial}); err != nil {
		t.Fatalf("DeleteVirtualMFADevice after deactivate: %v", err)
	}

	// Deleting a user that still has an enabled device is blocked; deleting a
	// clean user succeeds.
	guard, err := client.CreateVirtualMFADevice(ctx, &iam.CreateVirtualMFADeviceInput{
		VirtualMFADeviceName: aws.String("guard-mfa"),
	})
	if err != nil {
		t.Fatalf("CreateVirtualMFADevice (guard): %v", err)
	}

	if _, err := client.EnableMFADevice(ctx, &iam.EnableMFADeviceInput{
		UserName:            aws.String("lifecycle-user"),
		SerialNumber:        guard.VirtualMFADevice.SerialNumber,
		AuthenticationCode1: aws.String("111111"),
		AuthenticationCode2: aws.String("222222"),
	}); err != nil {
		t.Fatalf("EnableMFADevice (guard): %v", err)
	}

	_, err = client.DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String("lifecycle-user")})
	if err == nil {
		t.Fatal("DeleteUser with an enabled MFA device: expected error, got nil")
	}

	var dce2 *types.DeleteConflictException
	if !errors.As(err, &dce2) {
		t.Fatalf("delete-user-with-mfa error = %T, want *DeleteConflictException: %v", err, err)
	}
}

// TestSDKMFADeviceNotFoundErrors pins the NoSuchEntity error shape for the
// negative paths on each new operation.
func TestSDKMFADeviceNotFoundErrors(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("nf-user")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cases := map[string]func() error{
		"EnableMFADevice unknown device": func() error {
			_, err := client.EnableMFADevice(ctx, &iam.EnableMFADeviceInput{
				UserName:            aws.String("nf-user"),
				SerialNumber:        aws.String("arn:aws:iam::123456789012:mfa/ghost"),
				AuthenticationCode1: aws.String("111111"),
				AuthenticationCode2: aws.String("222222"),
			})
			return err
		},
		"DeactivateMFADevice unknown device": func() error {
			_, err := client.DeactivateMFADevice(ctx, &iam.DeactivateMFADeviceInput{
				UserName:     aws.String("nf-user"),
				SerialNumber: aws.String("arn:aws:iam::123456789012:mfa/ghost"),
			})
			return err
		},
		"DeleteVirtualMFADevice unknown device": func() error {
			_, err := client.DeleteVirtualMFADevice(ctx, &iam.DeleteVirtualMFADeviceInput{
				SerialNumber: aws.String("arn:aws:iam::123456789012:mfa/ghost"),
			})
			return err
		},
	}

	for name, do := range cases {
		t.Run(name, func(t *testing.T) {
			err := do()
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			var nse *types.NoSuchEntityException
			if !errors.As(err, &nse) {
				t.Fatalf("error = %T, want *NoSuchEntityException: %v", err, err)
			}
		})
	}
}

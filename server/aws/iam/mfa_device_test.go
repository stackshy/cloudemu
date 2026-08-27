package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
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

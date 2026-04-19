package ec2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
)

func TestRouteVolumesDispatch(t *testing.T) {
	h := newFullHandler()

	create := do(t, h, http.MethodPost, "/", url.Values{
		"Action":           {"CreateVolume"},
		"Size":             {"10"},
		"AvailabilityZone": {"us-east-1a"},
	})
	if create.Code != http.StatusOK {
		t.Fatalf("CreateVolume = %d: %s", create.Code, create.Body.String())
	}

	volID := between(create.Body.String(), "<volumeId>", "</volumeId>")
	if volID == "" {
		t.Fatalf("CreateVolume didn't return a volume id: %s", create.Body.String())
	}

	rr := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeVolumes"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeVolumes = %d", rr.Code)
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{"Action": {"DeleteVolume"}, "VolumeId": {volID}})
	if rr.Code != http.StatusOK {
		t.Errorf("DeleteVolume = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRouteVolumesAttachDetach(t *testing.T) {
	h := newFullHandler()

	instResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action":       {"RunInstances"},
		"ImageId":      {"ami-x"},
		"InstanceType": {"t2.micro"},
		"MinCount":     {"1"},
		"MaxCount":     {"1"},
	})

	instID := extractFirstInstanceID(instResp.Body.String())
	if instID == "" {
		t.Fatal("RunInstances didn't return instance id")
	}

	volResp := do(t, h, http.MethodPost, "/", url.Values{
		"Action":           {"CreateVolume"},
		"Size":             {"20"},
		"AvailabilityZone": {"us-east-1a"},
	})

	volID := between(volResp.Body.String(), "<volumeId>", "</volumeId>")

	attach := do(t, h, http.MethodPost, "/", url.Values{
		"Action":     {"AttachVolume"},
		"VolumeId":   {volID},
		"InstanceId": {instID},
		"Device":     {"/dev/sdf"},
	})
	if attach.Code != http.StatusOK {
		t.Errorf("AttachVolume = %d: %s", attach.Code, attach.Body.String())
	}

	detach := do(t, h, http.MethodPost, "/", url.Values{
		"Action":   {"DetachVolume"},
		"VolumeId": {volID},
	})
	if detach.Code != http.StatusOK {
		t.Errorf("DetachVolume = %d", detach.Code)
	}
}

func TestVolumeOpsUnknownIDReturnsError(t *testing.T) {
	h := newFullHandler()

	for _, form := range []url.Values{
		{"Action": {"DeleteVolume"}, "VolumeId": {"vol-ghost"}},
		{"Action": {"AttachVolume"}, "VolumeId": {"vol-ghost"},
			"InstanceId": {"i-ghost"}, "Device": {"/dev/sdf"}},
		{"Action": {"DetachVolume"}, "VolumeId": {"vol-ghost"}},
	} {
		rr := do(t, h, http.MethodPost, "/", form)
		if rr.Code == http.StatusOK {
			t.Errorf("%v should have errored, got 200: %s", form, rr.Body.String())
		}
	}
}

func TestRouteKeyPairsDispatch(t *testing.T) {
	h := newFullHandler()

	cre := do(t, h, http.MethodPost, "/", url.Values{
		"Action":  {"CreateKeyPair"},
		"KeyName": {"k1"},
	})
	if cre.Code != http.StatusOK {
		t.Fatalf("CreateKeyPair = %d", cre.Code)
	}

	// CreateKeyPair must return keyMaterial (private key).
	if !strings.Contains(cre.Body.String(), "<keyMaterial>") {
		t.Error("CreateKeyPair response missing <keyMaterial>")
	}

	rr := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeKeyPairs"}})
	if rr.Code != http.StatusOK {
		t.Errorf("DescribeKeyPairs = %d", rr.Code)
	}

	// DescribeKeyPairs must NOT include private-key material.
	if strings.Contains(rr.Body.String(), "<keyMaterial>") {
		t.Error("DescribeKeyPairs leaked <keyMaterial>")
	}

	rr = do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteKeyPair"}, "KeyName": {"k1"},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("DeleteKeyPair = %d", rr.Code)
	}
}

func TestKeyPairOpsUnknownIDReturnsError(t *testing.T) {
	h := newFullHandler()

	rr := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"DeleteKeyPair"}, "KeyName": {"ghost-key"},
	})
	if rr.Code == http.StatusOK {
		t.Errorf("DeleteKeyPair ghost-key should error, got 200")
	}
}

func TestToVolumeXMLStateDefault(t *testing.T) {
	in := &computedriver.VolumeInfo{ID: "vol-x", Size: 10}

	got := toVolumeXML(in)
	if got.Status != stateAvailable {
		t.Errorf("default status = %q, want %q", got.Status, stateAvailable)
	}

	if got.VolumeType != "" {
		t.Errorf("VolumeType should be empty when driver omits it, got %q", got.VolumeType)
	}

	if len(got.Attachments) != 0 {
		t.Errorf("unattached volume should have 0 attachments, got %d", len(got.Attachments))
	}
}

func TestToVolumeXMLWithAttachment(t *testing.T) {
	in := &computedriver.VolumeInfo{
		ID: "vol-x", Size: 20, State: "in-use",
		AttachedTo: "i-1", Device: "/dev/sdg",
	}

	got := toVolumeXML(in)
	if len(got.Attachments) != 1 {
		t.Fatalf("attached volume should have 1 attachment, got %d", len(got.Attachments))
	}

	if got.Attachments[0].InstanceID != "i-1" || got.Attachments[0].Device != "/dev/sdg" {
		t.Errorf("attachment fields wrong: %+v", got.Attachments[0])
	}
}

func TestToKeyPairSummaryXMLOmitsPrivateKey(t *testing.T) {
	in := &computedriver.KeyPairInfo{
		ID: "key-1", Name: "k", Fingerprint: "ff:ff", KeyType: "rsa",
		PublicKey: "ssh-rsa ...", PrivateKey: "SECRET",
	}

	got := toKeyPairSummaryXML(in)
	// keyPairSummaryXML has no PrivateKey/KeyMaterial field by design.
	if got.KeyName != "k" || got.KeyFingerprint != "ff:ff" {
		t.Errorf("summary wrong: %+v", got)
	}
}

func TestCreateVolumeDefaultsVolumeType(t *testing.T) {
	h := newFullHandler()

	cre := do(t, h, http.MethodPost, "/", url.Values{
		"Action":           {"CreateVolume"},
		"Size":             {"5"},
		"AvailabilityZone": {"us-east-1a"},
	})
	if cre.Code != http.StatusOK {
		t.Fatalf("CreateVolume = %d", cre.Code)
	}

	if !strings.Contains(cre.Body.String(), "<volumeType>"+defaultVolumeType+"</volumeType>") {
		t.Errorf("missing default volume type in response: %s", cre.Body.String())
	}
}

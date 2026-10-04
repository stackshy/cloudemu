package seed_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/seed"
)

func TestSeedIAMUserWithAccessKey(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	f, err := seed.Load([]byte(`{"iamUsers":[{"name":"admin","accessKeys":[{"accessKeyId":"AKIASEED","secretAccessKey":"seed-secret"}]}]}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if n := f.ResourceCount(); n != 2 {
		t.Fatalf("ResourceCount = %d, want 2 (user + key)", n)
	}

	target := seed.Target{IAM: cloud.IAM}
	if err := seed.Apply(ctx, f, target); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	ak, ok := cloud.IAM.AccessKeyByID(ctx, "AKIASEED")
	if !ok || ak.SecretAccessKey != "seed-secret" || ak.UserName != "admin" {
		t.Fatalf("seeded key = %+v ok=%v", ak, ok)
	}

	// Re-applying fails on the existing user unless IgnoreExisting is set.
	if err := seed.Apply(ctx, f, target); err == nil {
		t.Fatal("re-apply without IgnoreExisting: want AlreadyExists")
	}

	if err := seed.Apply(ctx, f, target, seed.IgnoreExisting()); err != nil {
		t.Fatalf("re-apply with IgnoreExisting: %v", err)
	}
}

func TestSeedIAMUserValidation(t *testing.T) {
	cloud := cloudemu.NewAWS()

	cases := map[string]struct {
		fixture string
		target  seed.Target
	}{
		"no IAM driver":  {`{"iamUsers":[{"name":"a"}]}`, seed.Target{}},
		"missing name":   {`{"iamUsers":[{"accessKeys":[]}]}`, seed.Target{IAM: cloud.IAM}},
		"missing secret": {`{"iamUsers":[{"name":"a","accessKeys":[{"accessKeyId":"AKIA"}]}]}`, seed.Target{IAM: cloud.IAM}},
		"no key import":  {`{"iamUsers":[{"name":"a","accessKeys":[{"accessKeyId":"K","secretAccessKey":"S"}]}]}`, seed.Target{IAM: cloudemu.NewGCP().IAM}},
	}

	for name, tc := range cases {
		f, err := seed.Load([]byte(tc.fixture))
		if err != nil {
			t.Fatalf("%s: Load: %v", name, err)
		}

		if err := seed.Apply(context.Background(), f, tc.target); err == nil {
			t.Errorf("%s: Apply = nil, want a validation error", name)
		}
	}
}

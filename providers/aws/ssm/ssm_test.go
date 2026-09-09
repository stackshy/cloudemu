package ssm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func newMock() *ssm.Mock {
	return ssm.New(config.NewOptions())
}

func TestPutOverwriteAndHistory(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	v, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/a", Value: "1", Type: driver.TypeString})
	if err != nil {
		t.Fatalf("PutParameter v1: %v", err)
	}

	if v != 1 {
		t.Fatalf("first version = %d, want 1", v)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/a", Value: "2", Type: driver.TypeString}); err == nil {
		t.Fatal("PutParameter without Overwrite: want AlreadyExists, got nil")
	} else if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}

	v2, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/a", Value: "2", Type: driver.TypeString, Overwrite: true})
	if err != nil {
		t.Fatalf("PutParameter overwrite: %v", err)
	}

	if v2 != 2 {
		t.Fatalf("overwrite version = %d, want 2", v2)
	}

	hist, err := m.GetParameterHistory(ctx, "/a", false)
	if err != nil {
		t.Fatalf("GetParameterHistory: %v", err)
	}

	if len(hist) != 2 || hist[0].Value != "1" || hist[1].Value != "2" {
		t.Fatalf("history = %+v, want [1 2]", hist)
	}
}

func TestPutOverwriteRetainsTypeWhenOmitted(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/secure", Value: "s1", Type: driver.TypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	// Overwrite without a Type must keep SecureString.
	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/secure", Value: "s2", Overwrite: true,
	}); err != nil {
		t.Fatalf("PutParameter(overwrite): %v", err)
	}

	got, err := m.GetParameter(ctx, "/secure", true)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.Type != driver.TypeSecureString {
		t.Fatalf("Type = %q, want SecureString (retained)", got.Type)
	}

	if got.Value != "s2" {
		t.Fatalf("Value = %q, want s2", got.Value)
	}
}

func TestPutOverwriteChangingTypeRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "v1", Type: driver.TypeString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	_, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "v2", Type: driver.TypeSecureString, Overwrite: true,
	})
	if err == nil {
		t.Fatal("PutParameter(change type): want error, got nil")
	}

	if !errors.Is(err, driver.ErrTypeMismatch) {
		t.Fatalf("want ErrTypeMismatch, got %v", err)
	}

	// The stored type must be unchanged.
	got, err := m.GetParameter(ctx, "/p", false)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.Type != driver.TypeString {
		t.Fatalf("Type = %q, want String (unchanged)", got.Type)
	}
}

func TestPutParameterInvalidTypeRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/bad", Value: "v", Type: "Bogus"})
	if err == nil {
		t.Fatal("PutParameter(invalid type): want error, got nil")
	}

	if !errors.Is(err, driver.ErrUnsupportedType) {
		t.Fatalf("want ErrUnsupportedType, got %v", err)
	}

	if _, err := m.GetParameter(ctx, "/bad", false); !cerrors.IsNotFound(err) {
		t.Fatalf("after rejected put, GetParameter err = %v, want NotFound", err)
	}
}

func TestPutParameterTagsOnCreate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/t/p", Value: "v", Type: driver.TypeString,
		Tags: map[string]string{"Env": "prod"},
	}); err != nil {
		t.Fatalf("PutParameter(create with tags): %v", err)
	}

	tags, err := m.ListParameterTags(ctx, "/t/p")
	if err != nil {
		t.Fatalf("ListParameterTags: %v", err)
	}

	if len(tags) != 1 || tags["Env"] != "prod" {
		t.Fatalf("tags = %v, want {Env:prod}", tags)
	}
}

func TestPutParameterOverwriteWithTagsRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/t/ot", Value: "v1", Type: driver.TypeString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	_, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/t/ot", Value: "v2", Overwrite: true,
		Tags: map[string]string{"K": "V"},
	})
	if err == nil {
		t.Fatal("PutParameter(overwrite+tags): want error, got nil")
	}

	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestLabelParameterVersionAndSelector(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/svc/cfg", Value: "old", Type: driver.TypeString}); err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/svc/cfg", Value: "new", Type: driver.TypeString, Overwrite: true}); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	// Label v1 "prod".
	applied, invalid, err := m.LabelParameterVersion(ctx, "/svc/cfg", 1, []string{"prod"})
	if err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}

	if applied != 1 || len(invalid) != 0 {
		t.Fatalf("applied=%d invalid=%v, want 1 []", applied, invalid)
	}

	got, err := m.GetParameter(ctx, "/svc/cfg:prod", false)
	if err != nil {
		t.Fatalf("GetParameter(:prod): %v", err)
	}

	if got.Value != "old" || got.Version != 1 {
		t.Fatalf("label selector got %q v%d, want old v1", got.Value, got.Version)
	}

	// Latest (no selector) is still v2.
	latest, err := m.GetParameter(ctx, "/svc/cfg", false)
	if err != nil {
		t.Fatalf("GetParameter(latest): %v", err)
	}

	if latest.Value != "new" || latest.Version != 2 {
		t.Fatalf("latest got %q v%d, want new v2", latest.Value, latest.Version)
	}
}

func TestGetParametersByPathRecursive(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, n := range []string{"/p/a", "/p/b", "/p/sub/c"} {
		if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: n, Value: "x", Type: driver.TypeString}); err != nil {
			t.Fatalf("Put %s: %v", n, err)
		}
	}

	shallow, err := m.GetParametersByPath(ctx, driver.GetByPathInput{Path: "/p"})
	if err != nil {
		t.Fatalf("GetParametersByPath: %v", err)
	}

	if len(shallow) != 2 {
		t.Fatalf("non-recursive returned %d, want 2", len(shallow))
	}

	deep, err := m.GetParametersByPath(ctx, driver.GetByPathInput{Path: "/p", Recursive: true})
	if err != nil {
		t.Fatalf("GetParametersByPath recursive: %v", err)
	}

	if len(deep) != 3 {
		t.Fatalf("recursive returned %d, want 3", len(deep))
	}
}

func TestGetParametersByPathTypeFilter(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/f/a", Value: "1", Type: driver.TypeString}); err != nil {
		t.Fatalf("Put /f/a: %v", err)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/f/b", Value: "x,y", Type: driver.TypeStringList}); err != nil {
		t.Fatalf("Put /f/b: %v", err)
	}

	got, err := m.GetParametersByPath(ctx, driver.GetByPathInput{
		Path: "/f",
		ParameterFilters: []driver.ParameterStringFilter{
			{Key: "Type", Option: "Equals", Values: []string{driver.TypeString}},
		},
	})
	if err != nil {
		t.Fatalf("GetParametersByPath(Type=String): %v", err)
	}

	if len(got) != 1 || got[0].Name != "/f/a" {
		t.Fatalf("got %+v, want only /f/a", paramNamesOf(got))
	}
}

func TestGetParametersByPathLabelFilter(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, n := range []string{"/g/a", "/g/b"} {
		if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: n, Value: "v", Type: driver.TypeString}); err != nil {
			t.Fatalf("Put %s: %v", n, err)
		}
	}

	if _, _, err := m.LabelParameterVersion(ctx, "/g/a", 0, []string{"prod"}); err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}

	got, err := m.GetParametersByPath(ctx, driver.GetByPathInput{
		Path: "/g",
		ParameterFilters: []driver.ParameterStringFilter{
			{Key: "Label", Option: "Equals", Values: []string{"prod"}},
		},
	})
	if err != nil {
		t.Fatalf("GetParametersByPath(Label=prod): %v", err)
	}

	if len(got) != 1 || got[0].Name != "/g/a" {
		t.Fatalf("got %v, want only /g/a", paramNamesOf(got))
	}
}

func TestGetParametersByPathInvalidFilter(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.GetParametersByPath(ctx, driver.GetByPathInput{
		Path:             "/f",
		ParameterFilters: []driver.ParameterStringFilter{{Key: "Name", Option: "Equals", Values: []string{"x"}}},
	})
	if !errors.Is(err, driver.ErrInvalidFilterKey) {
		t.Fatalf("unsupported key: got %v, want ErrInvalidFilterKey", err)
	}

	_, err = m.GetParametersByPath(ctx, driver.GetByPathInput{
		Path:             "/f",
		ParameterFilters: []driver.ParameterStringFilter{{Key: "Type", Option: "Contains", Values: []string{"String"}}},
	})
	if !errors.Is(err, driver.ErrInvalidFilterOption) {
		t.Fatalf("unsupported option: got %v, want ErrInvalidFilterOption", err)
	}
}

func paramNamesOf(ps []driver.Parameter) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}

	return out
}

// TestDeleteParameterStripsSelector covers #266: DeleteParameter strips a
// ":version"/":label" selector (like the read paths) so a selector addresses
// the base parameter rather than a literal name containing ':'.
func TestDeleteParameterStripsSelector(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/del/p", Value: "v", Type: driver.TypeString}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if err := m.DeleteParameter(ctx, "/del/p:1"); err != nil {
		t.Fatalf("DeleteParameter with selector: %v", err)
	}

	if _, err := m.GetParameter(ctx, "/del/p", false); !cerrors.IsNotFound(err) {
		t.Fatalf("after delete, GetParameter err = %v, want NotFound", err)
	}
}

func TestDeleteParameters(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/d/1", Value: "x", Type: driver.TypeString}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	deleted, invalid, err := m.DeleteParameters(ctx, []string{"/d/1", "/d/missing"})
	if err != nil {
		t.Fatalf("DeleteParameters: %v", err)
	}

	if len(deleted) != 1 || deleted[0] != "/d/1" {
		t.Fatalf("deleted = %v, want [/d/1]", deleted)
	}

	if len(invalid) != 1 || invalid[0] != "/d/missing" {
		t.Fatalf("invalid = %v, want [/d/missing]", invalid)
	}
}

// TestGetParameterSelectorIncludesColon covers a real-user e2e finding: real
// Parameter Store's GetParameter/GetParameters Selector field is ":<version>"
// or ":<label>" (colon included), not the bare version/label text.
func TestGetParameterSelectorIncludesColon(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/sel/p", Value: "v1", Type: driver.TypeString}); err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	if _, _, err := m.LabelParameterVersion(ctx, "/sel/p", 1, []string{"prod"}); err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}

	byVersion, err := m.GetParameter(ctx, "/sel/p:1", false)
	if err != nil {
		t.Fatalf("GetParameter(:1): %v", err)
	}

	if byVersion.Selector != ":1" {
		t.Fatalf("Selector = %q, want %q", byVersion.Selector, ":1")
	}

	byLabel, err := m.GetParameter(ctx, "/sel/p:prod", false)
	if err != nil {
		t.Fatalf("GetParameter(:prod): %v", err)
	}

	if byLabel.Selector != ":prod" {
		t.Fatalf("Selector = %q, want %q", byLabel.Selector, ":prod")
	}

	latest, err := m.GetParameter(ctx, "/sel/p", false)
	if err != nil {
		t.Fatalf("GetParameter(latest): %v", err)
	}

	if latest.Selector != "" {
		t.Fatalf("Selector = %q, want empty for an unselectored read", latest.Selector)
	}
}

// TestGetParametersAlphabeticalOrder covers a real-user e2e finding: real
// GetParameters always returns results in alphabetical order by name,
// regardless of the order Names was requested in.
func TestGetParametersAlphabeticalOrder(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, name := range []string{"/zzz/p", "/aaa/p", "/mid/p"} {
		if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: name, Value: "v", Type: driver.TypeString}); err != nil {
			t.Fatalf("Put %s: %v", name, err)
		}
	}

	found, _, err := m.GetParameters(ctx, []string{"/zzz/p", "/aaa/p", "/mid/p"}, false)
	if err != nil {
		t.Fatalf("GetParameters: %v", err)
	}

	got := paramNamesOf(found)
	want := []string{"/aaa/p", "/mid/p", "/zzz/p"}

	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v (alphabetical)", got, want)
		}
	}
}

// TestPutParameterValueSizeLimitByTier covers a real-user e2e finding: real
// Parameter Store rejects a Value over 4 KB on the (default) Standard tier and
// over 8 KB on the Advanced tier with ValidationException, instead of
// silently accepting an over-limit value.
func TestPutParameterValueSizeLimitByTier(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	standardOversized := make([]byte, driver.StandardTierMaxValueBytes+1)
	advancedOversized := make([]byte, driver.AdvancedTierMaxValueBytes+1)
	advancedFits := make([]byte, driver.StandardTierMaxValueBytes+1) // over Standard, under Advanced

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/size/std", Value: string(standardOversized), Type: driver.TypeString,
	}); !errors.Is(err, driver.ErrValueTooLarge) {
		t.Fatalf("Standard-tier oversized Put: want ErrValueTooLarge, got %v", err)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/size/adv", Value: string(advancedFits), Type: driver.TypeString, Tier: "Advanced",
	}); err != nil {
		t.Fatalf("Advanced-tier Put within limit: %v", err)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/size/adv2", Value: string(advancedOversized), Type: driver.TypeString, Tier: "Advanced",
	}); !errors.Is(err, driver.ErrValueTooLarge) {
		t.Fatalf("Advanced-tier oversized Put: want ErrValueTooLarge, got %v", err)
	}
}

// TestPutParameterReservedNamePrefixRejected covers a real-user e2e finding:
// real Parameter Store rejects a customer parameter name whose leading path
// segment is "aws" or "ssm" (case-insensitive) — that namespace is reserved
// for AWS-published parameters.
func TestPutParameterReservedNamePrefixRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, name := range []string{"aws.reserved", "/aws/reserved", "SSM.reserved", "/Ssm/reserved"} {
		if _, _, err := m.PutParameter(ctx, driver.PutConfig{
			Name: name, Value: "v", Type: driver.TypeString,
		}); !errors.Is(err, driver.ErrReservedNamePrefix) {
			t.Fatalf("PutParameter(%q): want ErrReservedNamePrefix, got %v", name, err)
		}
	}

	// A name that merely contains "aws"/"ssm" past the leading segment is fine.
	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/app/aws-region", Value: "v", Type: driver.TypeString,
	}); err != nil {
		t.Fatalf("PutParameter(/app/aws-region): %v", err)
	}
}

// TestPutOverwriteRetainsTierWhenOmittedAndRejectsDowngrade covers a
// real-user e2e finding: real Parameter Store retains an Advanced parameter's
// tier across an Overwrite that omits Tier (mirroring Type retention), and
// never lets an Advanced parameter silently revert to Standard.
func TestPutOverwriteRetainsTierWhenOmittedAndRejectsDowngrade(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, tier, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/tier/p", Value: "v1", Type: driver.TypeString, Tier: "Advanced",
	}); err != nil || tier != "Advanced" {
		t.Fatalf("create: tier=%q err=%v, want Advanced, nil", tier, err)
	}

	// Overwrite without Tier must retain Advanced, not reset to Standard.
	if _, tier, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/tier/p", Value: "v2", Type: driver.TypeString, Overwrite: true,
	}); err != nil || tier != "Advanced" {
		t.Fatalf("overwrite (no Tier): tier=%q err=%v, want Advanced, nil", tier, err)
	}

	// Explicitly requesting Standard on a currently-Advanced parameter is rejected.
	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/tier/p", Value: "v3", Type: driver.TypeString, Tier: "Standard", Overwrite: true,
	}); !errors.Is(err, driver.ErrCannotRevertTier) {
		t.Fatalf("overwrite (explicit Standard downgrade): want ErrCannotRevertTier, got %v", err)
	}

	// The tier must still be Advanced after the rejected downgrade attempt.
	got, err := m.GetParameter(ctx, "/tier/p", false)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.Value != "v2" || got.Version != 2 {
		t.Fatalf("after rejected downgrade: value=%q version=%d, want v2/2 (unchanged)", got.Value, got.Version)
	}
}

// toggleKMSCrypto is a minimal reversible KMSCrypto fake: Encrypt/Decrypt are
// identity transforms (so a value sealed while working can be unsealed later),
// but both fail once fail is set — modeling a key that becomes disabled after
// a value was already sealed under it.
type toggleKMSCrypto struct {
	fail bool
}

func (k *toggleKMSCrypto) Encrypt(_ context.Context, _ string, plaintext []byte) ([]byte, error) {
	if k.fail {
		return nil, cerrors.New(cerrors.FailedPrecondition, "key is disabled")
	}

	return plaintext, nil
}

func (k *toggleKMSCrypto) Decrypt(_ context.Context, ciphertext []byte) ([]byte, error) {
	if k.fail {
		return nil, cerrors.New(cerrors.FailedPrecondition, "key is disabled")
	}

	return ciphertext, nil
}

// TestSecureStringUnusableKMSKeyIsInvalidKeyID covers a real-user e2e finding:
// when the KMS key behind a SecureString can't be used, real Parameter Store
// surfaces the distinct InvalidKeyId error on PutParameter and on a decrypting
// GetParameter/GetParameterHistory read — not a generic internal error
// carrying the raw KMS failure message.
func TestSecureStringUnusableKMSKeyIsInvalidKeyID(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	kms := &toggleKMSCrypto{}
	m.SetKMSCrypto(kms)

	// Seal successfully while the key still works.
	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/secure/p", Value: "s1", Type: driver.TypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter(create): %v", err)
	}

	// The key becomes unusable (e.g. disabled) after the value was sealed.
	kms.fail = true

	if _, err := m.GetParameter(ctx, "/secure/p", true); !errors.Is(err, driver.ErrInvalidKeyID) {
		t.Fatalf("GetParameter(WithDecryption) with disabled key: want ErrInvalidKeyID, got %v", err)
	}

	// Reading without decryption is unaffected — it never calls into KMS.
	if _, err := m.GetParameter(ctx, "/secure/p", false); err != nil {
		t.Fatalf("GetParameter(no decryption) with disabled key: %v", err)
	}

	if _, err := m.GetParameterHistory(ctx, "/secure/p", true); !errors.Is(err, driver.ErrInvalidKeyID) {
		t.Fatalf("GetParameterHistory(WithDecryption) with disabled key: want ErrInvalidKeyID, got %v", err)
	}

	// A brand-new SecureString can't even be sealed while the key is unusable.
	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/secure/new", Value: "s2", Type: driver.TypeSecureString,
	}); !errors.Is(err, driver.ErrInvalidKeyID) {
		t.Fatalf("PutParameter with disabled key: want ErrInvalidKeyID, got %v", err)
	}
}

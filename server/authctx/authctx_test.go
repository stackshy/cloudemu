package authctx

import (
	"context"
	"testing"
)

func TestPrincipalRoundTrip(t *testing.T) {
	p := Principal{AccessKeyID: "AKIA1", UserName: "bob", ARN: "arn:aws:iam::1:user/bob", AccountID: "1"}
	ctx := WithPrincipal(context.Background(), p)

	got, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("PrincipalFrom: ok=false after WithPrincipal")
	}
	if got != p {
		t.Fatalf("round-trip mismatch: %+v != %+v", got, p)
	}
}

func TestPrincipalAbsent(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("PrincipalFrom on a bare context: want ok=false")
	}
}

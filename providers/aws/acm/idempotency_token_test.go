package acm_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

func TestRequestCertificateIdempotencyToken(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	in := driver.RequestCertificateInput{DomainName: "example.com", IdempotencyToken: "tok-1"}

	first, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	retry, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate retry: %v", err)
	}

	if retry != first {
		t.Fatalf("same-token retry ARN = %q, want original %q", retry, first)
	}

	in.IdempotencyToken = "tok-2"

	other, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate new token: %v", err)
	}

	if other == first {
		t.Fatalf("different token must mint a new certificate, got the same ARN %q", other)
	}

	list, err := m.ListCertificates(ctx, driver.ListFilter{})
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("certificate count = %d, want 2 (no duplicate on retry)", len(list))
	}
}

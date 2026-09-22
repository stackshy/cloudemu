package acm_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/acm"
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

func TestRequestCertificateTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	in := driver.RequestCertificateInput{DomainName: "example.com", IdempotencyToken: "g1"}

	first, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	if err = m.DeleteCertificate(ctx, first); err != nil {
		t.Fatalf("DeleteCertificate: %v", err)
	}

	// The certificate the token minted is gone: the token must not replay a ghost ARN.
	again, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate after delete: %v", err)
	}

	if again == first {
		t.Fatalf("same-token request after delete replayed the deleted ARN %q", first)
	}

	if _, err = m.DescribeCertificate(ctx, again); err != nil {
		t.Fatalf("certificate returned after delete must exist: %v", err)
	}
}

func TestRequestCertificateTokenLivesOneHour(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Unix(0, 0))
	m := acm.New(config.NewOptions(config.WithClock(clock)))

	in := driver.RequestCertificateInput{DomainName: "example.com", IdempotencyToken: "g1"}

	first, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	// ACM documents a one-hour token lifetime: 59 minutes later it still replays.
	clock.Advance(59 * time.Minute)

	retry, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate at 59m: %v", err)
	}

	if retry != first {
		t.Fatalf("token within its hour replayed %q, want %q", retry, first)
	}

	clock.Advance(2 * time.Minute)

	fresh, err := m.RequestCertificate(ctx, in)
	if err != nil {
		t.Fatalf("RequestCertificate at 61m: %v", err)
	}

	if fresh == first {
		t.Fatalf("token past its one-hour lifetime still replayed %q", first)
	}
}

func TestRequestCertificateTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	const n = 20

	arns := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			arns[i], errs[i] = m.RequestCertificate(ctx, driver.RequestCertificateInput{DomainName: "example.com", IdempotencyToken: "burst"})
		}()
	}

	close(start)
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}

		if arns[i] != arns[0] {
			t.Fatalf("call %d returned %q, want the single ARN %q", i, arns[i], arns[0])
		}
	}

	list, err := m.ListCertificates(ctx, driver.ListFilter{})
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("certificate count = %d, want exactly 1 for one token", len(list))
	}
}

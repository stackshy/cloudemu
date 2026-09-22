package globalaccelerator_test

import (
	"context"
	"testing"

	driver "github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

func TestCreateAcceleratorIdempotencyToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "tok-1"}

	first, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator")

	retry, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator retry")

	if retry.AcceleratorArn != first.AcceleratorArn {
		t.Fatalf("same-token retry arn = %s, want original %s", retry.AcceleratorArn, first.AcceleratorArn)
	}

	in.IdempotencyToken = "tok-2"

	other, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator new token")

	if other.AcceleratorArn == first.AcceleratorArn {
		t.Fatalf("different token must mint a new accelerator, got %s again", other.AcceleratorArn)
	}

	list, _, err := m.ListAccelerators(ctx, driver.Page{})
	requireNoError(t, err, "ListAccelerators")

	if len(list) != 2 {
		t.Fatalf("accelerator count = %d, want 2 (no duplicate on retry)", len(list))
	}
}

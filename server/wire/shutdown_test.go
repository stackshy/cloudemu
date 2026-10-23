package wire

import (
	"context"
	"testing"
)

func TestStreamContextEndsOnShutdown(t *testing.T) {
	shutdown, stop := context.WithCancel(context.Background())
	req := WithShutdown(context.Background(), shutdown)

	ctx, cancel := StreamContext(req)
	defer cancel()

	if ctx.Err() != nil {
		t.Fatal("stream context ended before shutdown")
	}

	stop()
	<-ctx.Done()
}

func TestStreamContextEndsWithRequest(t *testing.T) {
	req, end := context.WithCancel(context.Background())

	ctx, cancel := StreamContext(req)
	defer cancel()

	end()
	<-ctx.Done()
}

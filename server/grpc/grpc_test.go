package grpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1"

	cgrpc "github.com/stackshy/cloudemu/v2/server/grpc"
)

// serveEphemeral starts the transport on a loopback ephemeral port and returns
// the dial address plus a cleanup that gracefully shuts the server down.
func serveEphemeral(t *testing.T) (string, *cgrpc.Server) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := cgrpc.New()

	go func() {
		if serr := srv.Serve(ln); serr != nil {
			t.Errorf("Serve: %v", serr)
		}
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if serr := srv.Shutdown(ctx); serr != nil {
			t.Errorf("Shutdown: %v", serr)
		}
	})

	return ln.Addr().String(), srv
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// TestHealthCheck proves a real client can dial the transport and get SERVING
// from the standard grpc.health.v1.Health service, for both the whole-server
// (empty) name and a per-service status set by the caller.
func TestHealthCheck(t *testing.T) {
	addr, srv := serveEphemeral(t)
	srv.SetServingStatus("bigtable.admin.v2.BigtableTableAdmin", healthpb.HealthCheckResponse_SERVING)

	client := healthpb.NewHealthClient(dial(t, addr))

	cases := []struct {
		name    string
		service string
	}{
		{name: "whole server", service: ""},
		{name: "named service", service: "bigtable.admin.v2.BigtableTableAdmin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: tc.service})
			if err != nil {
				t.Fatalf("Check(%q): %v", tc.service, err)
			}

			if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				t.Fatalf("Check(%q) status = %v, want SERVING", tc.service, resp.GetStatus())
			}
		})
	}
}

// TestReflectionListsServices proves server reflection is registered and answers
// a ListServices request that includes the health and reflection services.
func TestReflectionListsServices(t *testing.T) {
	addr, _ := serveEphemeral(t)

	client := reflectpb.NewServerReflectionClient(dial(t, addr))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatalf("ServerReflectionInfo: %v", err)
	}

	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		t.Fatalf("send ListServices: %v", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}

	got := map[string]bool{}
	for _, s := range resp.GetListServicesResponse().GetService() {
		got[s.GetName()] = true
	}

	for _, want := range []string{
		"grpc.health.v1.Health",
		"grpc.reflection.v1.ServerReflection",
	} {
		if !got[want] {
			t.Errorf("reflection ListServices missing %q; got %v", want, got)
		}
	}
}

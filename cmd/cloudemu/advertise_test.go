package main

import (
	"reflect"
	"testing"
)

// The Kubernetes data plane self-advertises an endpoint (EKS/AKS/GKE
// DescribeCluster returns it). It must never advertise a bind-all address like
// 0.0.0.0 — that isn't connectable, so a kubeconfig pointing at it can't be
// dialed or TLS-verified. Under Docker (`--host 0.0.0.0`) it must fall back to a
// routable loopback.
func TestAdvertiseHostFor(t *testing.T) {
	cases := []struct {
		name, advertise, bind, want string
	}{
		{"docker all-interfaces -> loopback", "", "0.0.0.0", "127.0.0.1"},
		{"empty bind -> loopback", "", "", "127.0.0.1"},
		{"ipv6 unspecified -> loopback", "", "::", "127.0.0.1"},
		{"loopback bind kept", "", "127.0.0.1", "127.0.0.1"},
		{"specific ip kept", "", "192.168.1.5", "192.168.1.5"},
		{"explicit advertise wins over bind-all", "k8s.local", "0.0.0.0", "k8s.local"},
		{"explicit advertise wins over specific bind", "k8s.local", "10.0.0.2", "k8s.local"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := advertiseHostFor(tc.advertise, tc.bind); got != tc.want {
				t.Fatalf("advertiseHostFor(%q, %q) = %q, want %q", tc.advertise, tc.bind, got, tc.want)
			}
		})
	}
}

// k8sCertHosts must certify the advertised host + loopback names + extra
// --tls-host SANs, with no duplicates (so TLS verifies against whatever the
// endpoint advertises).
func TestK8sCertHosts(t *testing.T) {
	if got := k8sCertHosts("127.0.0.1", nil); !reflect.DeepEqual(got, []string{"127.0.0.1", "localhost"}) {
		t.Fatalf("loopback advertise: got %v, want [127.0.0.1 localhost] (deduped)", got)
	}

	got := k8sCertHosts("k8s.local", stringList{"192.168.1.5", "k8s.local"})
	want := []string{"k8s.local", "localhost", "127.0.0.1", "192.168.1.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("custom advertise + extras: got %v, want %v", got, want)
	}
}

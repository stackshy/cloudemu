package main

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/stackshy/cloudemu/v2/config"
)

// noDocker is a socket probe that reports Docker as unavailable, so a
// Docker-backed selection is forced down its degrade path regardless of the test
// host — deterministic without requiring (or forbidding) a real docker socket.
func noDocker() bool { return false }

// withDocker is the opposite probe, so a test can exercise the real-selection
// path without a live socket.
func withDocker() bool { return true }

// modeFor returns the MODE row for a capability, failing if it is missing.
func modeFor(t *testing.T, modes []engineMode, capability string) engineMode {
	t.Helper()

	for _, m := range modes {
		if m.capability == capability {
			return m
		}
	}

	t.Fatalf("no MODE row for capability %q", capability)

	return engineMode{}
}

// TestDockerBackedEngineDegradesWithoutSocket asserts that every Docker-backed
// selection degrades to in-memory (a MODE row, not an error) when the socket is
// absent, and that the degraded capability wires no real engine option.
func TestDockerBackedEngineDegradesWithoutSocket(t *testing.T) {
	cases := []struct {
		name string
		sel  engineSelection
		cap  string
	}{
		{"db=mysql", engineSelection{db: dbMySQL}, capDB},
		{"db=both", engineSelection{db: dbBoth}, capDB},
		{"compute=docker", engineSelection{compute: backingDocker}, capCompute},
		{"containers=docker", engineSelection{containers: backingDocker}, capContainers},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, modes, err := tc.sel.buildEngineOptions(noDocker)
			if err != nil {
				t.Fatalf("buildEngineOptions returned error instead of degrading: %v", err)
			}

			m := modeFor(t, modes, tc.cap)
			if m.status != modeMemory {
				t.Fatalf("%s status = %q, want %q", tc.cap, m.status, modeMemory)
			}

			if m.detail != reasonNoDocker {
				t.Fatalf("%s detail = %q, want %q", tc.cap, m.detail, reasonNoDocker)
			}

			// The degraded capability must wire no real engine.
			o := config.NewOptions(opts...)

			switch tc.cap {
			case capDB:
				if o.DatabaseEngine != nil {
					t.Fatalf("db degraded but DatabaseEngine is wired")
				}
			case capCompute:
				if o.ComputeEngine != nil {
					t.Fatalf("compute degraded but ComputeEngine is wired")
				}
			case capContainers:
				if o.ContainerEngine != nil {
					t.Fatalf("containers degraded but ContainerEngine is wired")
				}
			}
		})
	}
}

// TestDegradedEngineStillBoots proves a Docker-backed selection with the socket
// absent still boots the full server and the degraded capability works in-memory:
// the db degrades to memory, yet an RDS instance can be created and describes with
// a (synthetic) endpoint. The probe is forced false, so the test never requires
// Docker.
func TestDegradedEngineStillBoots(t *testing.T) {
	cfg := testConfig(t, engineSelection{db: dbMySQL})

	opts, modes, err := buildOptions(&cfg, noDocker)
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}

	if m := modeFor(t, modes, capDB); m.status != modeMemory {
		t.Fatalf("db status = %q, want %q", m.status, modeMemory)
	}

	awsURL, stop := startAWS(t, cfg, opts)
	defer stop()

	client := rdsClient(t, awsURL)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("degraded-db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		MasterUsername:       aws.String("u"),
		MasterUserPassword:   aws.String("password-123"),
	}); err != nil {
		t.Fatalf("CreateDBInstance on degraded (in-memory) db: %v", err)
	}

	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("degraded-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(desc.DBInstances) != 1 || desc.DBInstances[0].Endpoint == nil {
		t.Fatalf("degraded db reported no endpoint: %+v", desc.DBInstances)
	}
}

// TestStorageEngineWiresOption proves --storage=localfs wires a config.StorageEngine
// into the built options and records a real MODE row — a unit-level check that
// needs no S3 SDK.
func TestStorageEngineWiresOption(t *testing.T) {
	sel := engineSelection{storage: storageLocalFS, storageDir: t.TempDir()}

	opts, modes, err := sel.buildEngineOptions(withDocker)
	if err != nil {
		t.Fatalf("buildEngineOptions: %v", err)
	}

	if o := config.NewOptions(opts...); o.StorageEngine == nil {
		t.Fatalf("storage=localfs did not wire a StorageEngine into the options")
	}

	if m := modeFor(t, modes, capStorage); m.status != modeReal {
		t.Fatalf("storage status = %q, want %q", m.status, modeReal)
	}
}

// TestStorageEngineOffByDefault proves the default selection wires no storage
// engine, keeping object bytes in-memory.
func TestStorageEngineOffByDefault(t *testing.T) {
	sel := allEnginesOff()

	opts, modes, err := sel.buildEngineOptions(withDocker)
	if err != nil {
		t.Fatalf("buildEngineOptions: %v", err)
	}

	if o := config.NewOptions(opts...); o.StorageEngine != nil {
		t.Fatalf("storage engine wired with no --storage flag")
	}

	if m := modeFor(t, modes, capStorage); m.status != modeOff {
		t.Fatalf("storage status = %q, want %q", m.status, modeOff)
	}
}

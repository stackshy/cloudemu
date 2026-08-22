package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/stackshy/cloudemu/v2/config"
	dockercompute "github.com/stackshy/cloudemu/v2/contrib/dockerengine/compute"
	dockercontainer "github.com/stackshy/cloudemu/v2/contrib/dockerengine/container"
	dockermysql "github.com/stackshy/cloudemu/v2/contrib/dockerengine/mysql"
	realfunctions "github.com/stackshy/cloudemu/v2/contrib/realengine/functions"
	realpostgres "github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
	realredis "github.com/stackshy/cloudemu/v2/contrib/realengine/redis"
	"github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
)

// Engine selection values accepted on the flags. All default to "off", so no
// flags reproduce the pure in-memory `cloudemu serve`.
const (
	engineOff = "off"

	dbPostgres = "postgres"
	dbMySQL    = "mysql"
	dbBoth     = "both"

	cacheRedis = "redis"

	functionsSubprocess = "subprocess"

	backingDocker = "docker"
)

// defaultEnginePort lets a Docker/embedded engine pick its standard port.
const defaultEnginePort = 0

// Engine-selection errors. Static so err113 stays satisfied; the offending
// value is wrapped in at the call site.
var (
	// errDockerUnavailable reports a Docker-backed engine was requested but the
	// docker CLI is not on PATH.
	errDockerUnavailable = errors.New("docker CLI not found on PATH — required for the selected engine")
	errInvalidDB         = errors.New("invalid --db value (want off|postgres|mysql|both)")
	errInvalidCompute    = errors.New("invalid --compute value (want off|docker)")
	errInvalidContainers = errors.New("invalid --containers value (want off|docker)")
)

// engineSelection is the resolved engine choice for each capability.
type engineSelection struct {
	db         string
	cache      string
	functions  string
	compute    string
	containers string
}

// applyAllReal turns on the batteries-included real-engine set (Postgres, Redis,
// subprocess functions, Docker compute + containers) for any capability still
// left off.
func (s *engineSelection) applyAllReal() {
	if s.db == engineOff {
		s.db = dbPostgres
	}

	if s.cache == engineOff {
		s.cache = cacheRedis
	}

	if s.functions == engineOff {
		s.functions = functionsSubprocess
	}

	if s.compute == engineOff {
		s.compute = backingDocker
	}

	if s.containers == engineOff {
		s.containers = backingDocker
	}
}

// buildEngineOptions turns the selection into config.Options that wire the
// chosen real engines into every cloud. An empty selection returns no options,
// leaving the emulator fully in-memory.
func (s *engineSelection) buildEngineOptions() ([]config.Option, error) {
	var opts []config.Option

	if opt, err := databaseOption(s.db); err != nil {
		return nil, err
	} else if opt != nil {
		opts = append(opts, opt)
	}

	if s.cache == cacheRedis {
		opts = append(opts, config.WithCacheEngine(realredis.New()))
	}

	if s.functions == functionsSubprocess {
		opts = append(opts, config.WithFunctionEngine(realfunctions.New()))
	}

	if opt, err := computeOption(s.compute); err != nil {
		return nil, err
	} else if opt != nil {
		opts = append(opts, opt)
	}

	if opt, err := containerOption(s.containers); err != nil {
		return nil, err
	} else if opt != nil {
		opts = append(opts, opt)
	}

	return opts, nil
}

// databaseOption builds the database-engine option for the selection.
func databaseOption(v string) (config.Option, error) {
	switch v {
	case engineOff:
		return nil, nil
	case dbPostgres:
		return config.WithDatabaseEngine(realpostgres.New(defaultEnginePort)), nil
	case dbMySQL:
		if !dockerAvailable() {
			return nil, fmt.Errorf("--db=mysql: %w", errDockerUnavailable)
		}

		return config.WithDatabaseEngine(dockermysql.New(defaultEnginePort)), nil
	case dbBoth:
		if !dockerAvailable() {
			return nil, fmt.Errorf("--db=both: %w", errDockerUnavailable)
		}

		pg, my := realpostgres.New(defaultEnginePort), dockermysql.New(defaultEnginePort)
		eng := closableDB{
			DatabaseEngine: dbengine.NewMultiEngine(
				dbengine.FamilyEngine{Match: dbengine.IsMySQLFamily, Engine: my},
				dbengine.FamilyEngine{Match: dbengine.IsPostgresFamily, Engine: pg},
			),
			backings: []io.Closer{pg, my},
		}

		return config.WithDatabaseEngine(eng), nil
	default:
		return nil, fmt.Errorf("%w: %q", errInvalidDB, v)
	}
}

// computeOption builds the compute-engine option for the selection.
func computeOption(v string) (config.Option, error) {
	switch v {
	case engineOff:
		return nil, nil
	case backingDocker:
		if !dockerAvailable() {
			return nil, fmt.Errorf("--compute=docker: %w", errDockerUnavailable)
		}

		return config.WithComputeEngine(dockercompute.New()), nil
	default:
		return nil, fmt.Errorf("%w: %q", errInvalidCompute, v)
	}
}

// containerOption builds the container-engine option for the selection.
func containerOption(v string) (config.Option, error) {
	switch v {
	case engineOff:
		return nil, nil
	case backingDocker:
		if !dockerAvailable() {
			return nil, fmt.Errorf("--containers=docker: %w", errDockerUnavailable)
		}

		return config.WithContainerEngine(dockercontainer.New()), nil
	default:
		return nil, fmt.Errorf("%w: %q", errInvalidContainers, v)
	}
}

// dockerAvailable reports whether the docker CLI is on PATH. The dockerengine
// module gates on this too, but its check lives in an internal package that
// another module cannot import, so this mirrors that one-line probe.
func dockerAvailable() bool {
	_, err := exec.LookPath("docker")

	return err == nil
}

// closableDB adds Close over the two backings behind dbengine.NewMultiEngine:
// the composite dispatcher is a core type that (correctly) does not import
// contrib and so exposes no Close, but the server owns the real Postgres/MySQL
// engines it built and must tear them down on shutdown.
type closableDB struct {
	config.DatabaseEngine // the family dispatcher from dbengine.NewMultiEngine

	backings []io.Closer
}

// Close stops both backing engines. Safe to call more than once.
func (c closableDB) Close() error {
	errs := make([]error, 0, len(c.backings))

	for _, b := range c.backings {
		errs = append(errs, b.Close())
	}

	return errors.Join(errs...)
}

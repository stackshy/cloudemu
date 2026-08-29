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
	"github.com/stackshy/cloudemu/v2/contrib/realengine/blobstore"
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

	storageLocalFS = "localfs"
)

// Capability names, used as the left column of the startup MODE banner.
const (
	capDB         = "db"
	capCache      = "cache"
	capFunctions  = "functions"
	capCompute    = "compute"
	capContainers = "containers"
	capStorage    = "storage"
)

// MODE statuses reported per capability in the startup banner.
const (
	modeOff    = "off"                 // no real engine selected — in-memory
	modeReal   = "real"                // the selected real engine is wired
	modeMemory = "fell-back-to-memory" // selected but unavailable, degraded
)

// reasonNoDocker is the degrade reason when a Docker-backed engine is selected
// but the docker socket/CLI is absent.
const reasonNoDocker = "no docker socket"

// defaultEnginePort lets a Docker/embedded engine pick its standard port.
const defaultEnginePort = 0

// Engine-selection errors. Static so err113 stays satisfied; the offending
// value is wrapped in at the call site. Docker absence is NOT an error — a
// Docker-backed selection degrades to in-memory instead (see buildEngineOptions).
var (
	errInvalidDB         = errors.New("invalid --db value (want off|postgres|mysql|both)")
	errInvalidCompute    = errors.New("invalid --compute value (want off|docker)")
	errInvalidContainers = errors.New("invalid --containers value (want off|docker)")
	errInvalidStorage    = errors.New("invalid --storage value (want off|localfs)")
)

// engineSelection is the resolved engine choice for each capability.
type engineSelection struct {
	db         string
	cache      string
	functions  string
	compute    string
	containers string
	storage    string
	storageDir string // root dir for --storage=localfs; empty picks a temp dir
}

// engineMode is one capability's resolved backing for the startup MODE banner:
// what it ended up as (real / fell-back-to-memory / off) and a short detail
// (the engine description, or the fallback reason).
type engineMode struct {
	capability string
	status     string
	detail     string
}

// applyAllReal turns on the batteries-included real-engine set (Postgres, Redis,
// subprocess functions, Docker compute + containers, localfs storage) for any
// capability still left off.
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

	if s.storage == engineOff {
		s.storage = storageLocalFS
	}
}

// buildEngineOptions turns the selection into config.Options that wire the
// chosen real engines into every cloud, plus a per-capability MODE table for the
// startup banner. An empty selection returns no options, leaving the emulator
// fully in-memory. A Docker-backed engine selected while dockerAvailable reports
// false degrades to in-memory (a MODE row, not an error) so a socket-less run
// still boots. Genuinely invalid flag values return an error.
func (s *engineSelection) buildEngineOptions(dockerAvailable func() bool) ([]config.Option, []engineMode, error) {
	var (
		opts  []config.Option
		modes []engineMode
	)

	// add records one capability that cannot fail (cache/functions/off).
	add := func(opt config.Option, m engineMode) {
		if opt != nil {
			opts = append(opts, opt)
		}

		modes = append(modes, m)
	}

	// addErr records one capability whose flag value may be invalid.
	addErr := func(opt config.Option, m engineMode, err error) error {
		if err != nil {
			return err
		}

		add(opt, m)

		return nil
	}

	// A zero-value selection field means "off" (in-memory): the flag path always
	// resolves to "off", but a directly-constructed selection may leave it empty.
	if err := addErr(databaseOption(orOff(s.db), dockerAvailable)); err != nil {
		return nil, nil, err
	}

	add(cacheOption(orOff(s.cache)))
	add(functionsOption(orOff(s.functions)))

	if err := addErr(computeOption(orOff(s.compute), dockerAvailable)); err != nil {
		return nil, nil, err
	}

	if err := addErr(containerOption(orOff(s.containers), dockerAvailable)); err != nil {
		return nil, nil, err
	}

	if err := addErr(storageOption(orOff(s.storage), s.storageDir)); err != nil {
		return nil, nil, err
	}

	return opts, modes, nil
}

// databaseOption builds the database-engine option for the selection. mysql/both
// need Docker; without it they degrade to in-memory.
func databaseOption(v string, dockerAvailable func() bool) (config.Option, engineMode, error) {
	switch v {
	case engineOff:
		return nil, engineMode{capDB, modeOff, ""}, nil
	case dbPostgres:
		return config.WithDatabaseEngine(realpostgres.New(defaultEnginePort)),
			engineMode{capDB, modeReal, "embedded postgres"}, nil
	case dbMySQL:
		if !dockerAvailable() {
			return nil, engineMode{capDB, modeMemory, reasonNoDocker}, nil
		}

		return config.WithDatabaseEngine(dockermysql.New(defaultEnginePort)),
			engineMode{capDB, modeReal, "docker mysql"}, nil
	case dbBoth:
		if !dockerAvailable() {
			return nil, engineMode{capDB, modeMemory, reasonNoDocker}, nil
		}

		pg, my := realpostgres.New(defaultEnginePort), dockermysql.New(defaultEnginePort)
		eng := closableDB{
			DatabaseEngine: dbengine.NewMultiEngine(
				dbengine.FamilyEngine{Match: dbengine.IsMySQLFamily, Engine: my},
				dbengine.FamilyEngine{Match: dbengine.IsPostgresFamily, Engine: pg},
			),
			backings: []io.Closer{pg, my},
		}

		return config.WithDatabaseEngine(eng), engineMode{capDB, modeReal, "embedded postgres + docker mysql"}, nil
	default:
		return nil, engineMode{}, fmt.Errorf("%w: %q", errInvalidDB, v)
	}
}

// cacheOption builds the cache-engine option. Only redis is backed (miniredis,
// no Docker); any other non-off value leaves the cache in-memory.
func cacheOption(v string) (config.Option, engineMode) {
	if v == cacheRedis {
		return config.WithCacheEngine(realredis.New()), engineMode{capCache, modeReal, "miniredis"}
	}

	return nil, engineMode{capCache, modeOff, ""}
}

// functionsOption builds the function-engine option. Only subprocess is backed
// (runs real python3/node, no Docker); any other non-off value leaves functions
// stubbed.
func functionsOption(v string) (config.Option, engineMode) {
	if v == functionsSubprocess {
		return config.WithFunctionEngine(realfunctions.New()), engineMode{capFunctions, modeReal, "subprocess (python3/node)"}
	}

	return nil, engineMode{capFunctions, modeOff, ""}
}

// computeOption builds the compute-engine option for the selection. docker
// degrades to in-memory when the socket is absent.
func computeOption(v string, dockerAvailable func() bool) (config.Option, engineMode, error) {
	switch v {
	case engineOff:
		return nil, engineMode{capCompute, modeOff, ""}, nil
	case backingDocker:
		if !dockerAvailable() {
			return nil, engineMode{capCompute, modeMemory, reasonNoDocker}, nil
		}

		return config.WithComputeEngine(dockercompute.New()), engineMode{capCompute, modeReal, "docker"}, nil
	default:
		return nil, engineMode{}, fmt.Errorf("%w: %q", errInvalidCompute, v)
	}
}

// containerOption builds the container-engine option for the selection. docker
// degrades to in-memory when the socket is absent.
func containerOption(v string, dockerAvailable func() bool) (config.Option, engineMode, error) {
	switch v {
	case engineOff:
		return nil, engineMode{capContainers, modeOff, ""}, nil
	case backingDocker:
		if !dockerAvailable() {
			return nil, engineMode{capContainers, modeMemory, reasonNoDocker}, nil
		}

		return config.WithContainerEngine(dockercontainer.New()), engineMode{capContainers, modeReal, "docker"}, nil
	default:
		return nil, engineMode{}, fmt.Errorf("%w: %q", errInvalidContainers, v)
	}
}

// storageOption builds the storage-engine option for the selection. localfs
// persists object bytes to a real local filesystem (no Docker); an empty dir
// picks a temp directory.
func storageOption(v, dir string) (config.Option, engineMode, error) {
	switch v {
	case engineOff:
		return nil, engineMode{capStorage, modeOff, ""}, nil
	case storageLocalFS:
		detail := "localfs"
		if dir != "" {
			detail = "localfs " + dir
		}

		return config.WithStorageEngine(blobstore.New(dir)), engineMode{capStorage, modeReal, detail}, nil
	default:
		return nil, engineMode{}, fmt.Errorf("%w: %q", errInvalidStorage, v)
	}
}

// orOff maps an empty selection value to "off", so a zero-value engineSelection
// field is treated as in-memory rather than an invalid value.
func orOff(v string) string {
	if v == "" {
		return engineOff
	}

	return v
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

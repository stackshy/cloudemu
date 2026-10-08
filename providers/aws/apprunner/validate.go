package apprunner

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// Documented App Runner name, range and enum rules.
const (
	minNameLen           = 4
	maxServiceNameLen    = 40
	maxConfigNameLen     = 32
	maxResourceNameLen   = 40
	minMaxConcurrency    = 1
	maxMaxConcurrency    = 200
	maxMinSize           = 25
	minHealthSetting     = 1
	maxHealthSetting     = 20
	maxListResults       = 100
	maxImageIdentifier   = 1024
	defaultCPU           = "1024"
	defaultMemory        = "2048"
	defaultHealthPath    = "/"
	defaultHealthProto   = "TCP"
	defaultHealthTimeout = 2
	defaultHealthEvery   = 5
	defaultHealthy       = 1
	defaultUnhealthy     = 5
)

var (
	serviceNameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{3,39}$`)
	configNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{3,31}$`)
	resourceNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{3,39}$`)
	cpuRE          = regexp.MustCompile(`^(256|512|1024|2048|4096|(0\.25|0\.5|1|2|4) vCPU)$`)
	memoryRE       = regexp.MustCompile(`^(512|1024|2048|3072|4096|6144|8192|10240|12288|(0\.5|1|2|3|4|6|8|10|12) GB)$`)
	domainRE       = regexp.MustCompile(`^[A-Za-z0-9*.-]{1,255}$`)
)

func validateServiceName(name string) error {
	if !serviceNameRE.MatchString(name) {
		return invalidRequest("ServiceName must be 4-40 characters, start with a letter or digit and contain only " +
			"letters, digits, hyphens and underscores")
	}

	return nil
}

// validateConfigName checks the 4-32 character name of an auto scaling or
// observability configuration.
func validateConfigName(field, name string) error {
	if !configNameRE.MatchString(name) {
		return invalidRequest(field + " must be 4-32 characters, start with a letter or digit and contain only " +
			"letters, digits, hyphens and underscores")
	}

	return nil
}

func validateResourceName(field, name string) error {
	if !resourceNameRE.MatchString(name) {
		return invalidRequest(field + " must be 4-40 characters, start with a letter or digit and contain only " +
			"letters, digits, hyphens and underscores")
	}

	return nil
}

// validateSource requires exactly one of a code or an image repository, with the
// members the API requires.
func validateSource(in *driver.SourceConfiguration) error {
	if in == nil || (in.CodeRepository == nil) == (in.ImageRepository == nil) {
		return invalidRequest("SourceConfiguration must specify exactly one of CodeRepository or ImageRepository")
	}

	if img := in.ImageRepository; img != nil {
		if img.ImageIdentifier == "" || len(img.ImageIdentifier) > maxImageIdentifier {
			return invalidRequest("ImageRepository.ImageIdentifier is required (at most 1024 characters)")
		}

		if img.ImageRepositoryType != "ECR" && img.ImageRepositoryType != "ECR_PUBLIC" {
			return invalidRequest("ImageRepository.ImageRepositoryType must be ECR or ECR_PUBLIC")
		}

		return nil
	}

	return validateCodeRepository(in.CodeRepository)
}

// validateCodeRepository checks the members a code source requires.
func validateCodeRepository(code *driver.CodeRepository) error {
	if code.RepositoryURL == "" {
		return invalidRequest("CodeRepository.RepositoryUrl is required")
	}

	if v := code.SourceCodeVersion; v == nil || v.Type != "BRANCH" || v.Value == "" {
		return invalidRequest("CodeRepository.SourceCodeVersion is required with Type BRANCH and a Value")
	}

	if c := code.CodeConfiguration; c != nil && c.ConfigurationSource != "REPOSITORY" && c.ConfigurationSource != "API" {
		return invalidRequest("CodeConfiguration.ConfigurationSource must be REPOSITORY or API")
	}

	return nil
}

// resolveInstanceConfiguration validates a supplied instance configuration and
// fills the defaults (1 vCPU / 2 GB) for unset members.
func resolveInstanceConfiguration(in *driver.InstanceConfiguration) (*driver.InstanceConfiguration, error) {
	out := copyInstanceConfiguration(in)
	if out == nil {
		out = &driver.InstanceConfiguration{}
	}

	if out.CPU == "" {
		out.CPU = defaultCPU
	}

	if out.Memory == "" {
		out.Memory = defaultMemory
	}

	if !cpuRE.MatchString(out.CPU) {
		return nil, invalidRequest("InstanceConfiguration.Cpu must be 256, 512, 1024, 2048, 4096 or 0.25, 0.5, 1, 2, 4 vCPU")
	}

	if !memoryRE.MatchString(out.Memory) {
		return nil, invalidRequest("InstanceConfiguration.Memory must be 512 MB to 12 GB in the documented steps")
	}

	if !validInstancePair(out.CPU, out.Memory) {
		return nil, invalidRequest("InstanceConfiguration.Cpu " + out.CPU + " does not support Memory " + out.Memory +
			": the documented pairs are 0.25 vCPU with 0.5 GB, 0.5 with 1, 1 with 2/3/4, 2 with 4/6 and 4 with 8/10/12")
	}

	return out, nil
}

// resolveHealthCheck validates a supplied health check configuration and fills
// the documented defaults (TCP, interval 5, timeout 2, thresholds 1 and 5, path /).
func resolveHealthCheck(in *driver.HealthCheckConfiguration) (*driver.HealthCheckConfiguration, error) {
	out := copyHealthCheck(in)
	if out == nil {
		out = &driver.HealthCheckConfiguration{}
	}

	if out.Protocol == "" {
		out.Protocol = defaultHealthProto
	}

	if out.Protocol != "TCP" && out.Protocol != "HTTP" {
		return nil, invalidRequest("HealthCheckConfiguration.Protocol must be TCP or HTTP")
	}

	if out.Path == "" {
		out.Path = defaultHealthPath
	}

	for _, s := range []struct {
		name string
		v    **int32
		def  int32
	}{
		{"Interval", &out.Interval, defaultHealthEvery}, {"Timeout", &out.Timeout, defaultHealthTimeout},
		{"HealthyThreshold", &out.HealthyThreshold, defaultHealthy}, {"UnhealthyThreshold", &out.UnhealthyThreshold, defaultUnhealthy},
	} {
		if *s.v == nil {
			d := s.def
			*s.v = &d
		}

		if n := **s.v; n < minHealthSetting || n > maxHealthSetting {
			return nil, invalidRequest("HealthCheckConfiguration." + s.name + " must be between 1 and 20")
		}
	}

	return out, nil
}

// validatePage rejects a MaxResults outside the documented 1..100.
func validatePage(page driver.Page) error {
	if page.MaxResults != 0 && (page.MaxResults < 1 || page.MaxResults > maxListResults) {
		return invalidRequest("MaxResults must be between 1 and 100")
	}

	return nil
}

func validateDomainName(name string) error {
	if !domainRE.MatchString(name) {
		return invalidRequest("DomainName must be 1-255 characters of letters, digits, '*', '.' and '-'")
	}

	return nil
}

// instancePairs lists the memory sizes (MB) each CPU size (units) supports.
//
//nolint:gochecknoglobals // immutable lookup table of the documented CPU/memory pairs
var instancePairs = map[int][]int{
	256: {512, 1024}, 512: {1024}, 1024: {2048, 3072, 4096}, 2048: {4096, 6144}, 4096: {8192, 10240, 12288},
}

// cpuUnits converts a Cpu value ("1024" or "1 vCPU") to CPU units; 0 when unknown.
func cpuUnits(cpu string) int {
	if v, ok := strings.CutSuffix(cpu, " vCPU"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}

		return int(f * cpuUnitsPerVCPU)
	}

	n, _ := strconv.Atoi(cpu)

	return n
}

// memoryMB converts a Memory value ("2048" or "2 GB") to megabytes; 0 when unknown.
func memoryMB(mem string) int {
	if v, ok := strings.CutSuffix(mem, " GB"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}

		return int(f * mbPerGB)
	}

	n, _ := strconv.Atoi(mem)

	return n
}

const (
	cpuUnitsPerVCPU = 1024
	mbPerGB         = 1024
)

// validInstancePair reports whether the memory size is one the CPU size supports.
func validInstancePair(cpu, mem string) bool {
	return slices.Contains(instancePairs[cpuUnits(cpu)], memoryMB(mem))
}

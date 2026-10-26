package driver

// DomainRef names a domain and, optionally, the account that owns it. An empty
// Owner means the caller's own account.
type DomainRef struct {
	Domain string
	Owner  string
}

// RepositoryRef names a repository inside a domain.
type RepositoryRef struct {
	Domain     string
	Owner      string
	Repository string
}

// PackageRef names a package in a repository by its coordinates. Namespace is
// empty for formats that have none.
type PackageRef struct {
	Domain     string
	Owner      string
	Repository string
	Format     string
	Namespace  string
	Package    string
}

// Package formats (the PackageFormat enum of the CodeArtifact API).
const (
	FormatNPM     = "npm"
	FormatPyPI    = "pypi"
	FormatMaven   = "maven"
	FormatNuGet   = "nuget"
	FormatGeneric = "generic"
	FormatRuby    = "ruby"
	FormatSwift   = "swift"
	FormatCargo   = "cargo"
)

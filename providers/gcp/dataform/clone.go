package dataform

import dfdriver "github.com/stackshy/cloudemu/v2/services/dataform/driver"

// cloneStrMap deep-copies a label map so a stored value is never aliased by one
// handed back to a caller. An empty map clones to nil so round-tripped resources
// compare cleanly.
func cloneStrMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// cloneRaw deep-copies a raw JSON block. Empty input clones to nil so an unset
// block is omitted from the wire output and never drifts.
func cloneRaw(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}

	out := make([]byte, len(in))
	copy(out, in)

	return out
}

// cloneRepository returns a deep copy of repo.
func cloneRepository(repo *dfdriver.Repository) dfdriver.Repository {
	out := *repo
	out.Labels = cloneStrMap(repo.Labels)
	out.GitRemoteSettings = cloneRaw(repo.GitRemoteSettings)
	out.WorkspaceCompilationOverrides = cloneRaw(repo.WorkspaceCompilationOverrides)

	return out
}

package lambda

import (
	"archive/zip"
	"bytes"
	"io"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// maxLayerFileBytes caps a single overlaid file, matching the engine's per-entry
// unzip limit, so a crafted layer zip can't expand without bound during merge.
const maxLayerFileBytes = 64 << 20 // 64 MiB

// layerKey identifies a stored layer version's content by name and version.
func layerKey(name string, version int) string {
	return name + ":" + strconv.Itoa(version)
}

// putLayerContent stages a published layer version's zip bytes so a function
// that imports the layer can have its files overlaid into the deployment
// package. Empty content (an S3-sourced layer we did not fetch) is not stored.
func (h *Handler) putLayerContent(name string, version int, content []byte) {
	if len(content) == 0 {
		return
	}

	h.mu.Lock()
	h.layerContent[layerKey(name, version)] = content
	h.mu.Unlock()
}

// layerContentFor returns the staged content for a layer version ARN, or nil
// when the layer was not published to this emulator.
func (h *Handler) layerContentFor(arn string) []byte {
	name, version, ok := parseLayerARN(arn)
	if !ok {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	return h.layerContent[layerKey(name, version)]
}

// parseLayerARN extracts the layer name and version from a layer-version ARN
// (arn:aws:lambda:<region>:<account>:layer:<name>:<version>).
func parseLayerARN(arn string) (name string, version int, ok bool) {
	const marker = ":layer:"

	i := strings.Index(arn, marker)
	if i < 0 {
		return "", 0, false
	}

	rest := arn[i+len(marker):] // <name>:<version>

	j := strings.LastIndex(rest, ":")
	if j < 0 {
		return "", 0, false
	}

	version, err := strconv.Atoi(rest[j+1:])
	if err != nil {
		return "", 0, false
	}

	return rest[:j], version, true
}

// overlayLayers merges each configured layer version's staged zip content into
// the function's deployment package so imports from a pure-code layer resolve at
// real invoke. Real Lambda extracts layers under /opt; the subprocess engine
// runs the function from a single directory, so layer files are overlaid
// alongside the function code (a known runtime prefix like "python/" is stripped
// so both root-shaped and /opt-shaped layers resolve). Function files win on a
// name clash; earlier layers win over later ones, mirroring Lambda precedence.
// Layers whose content was not published to this emulator are skipped.
func (h *Handler) overlayLayers(code []byte, layers []string) ([]byte, error) {
	if len(code) == 0 || len(layers) == 0 {
		return code, nil
	}

	contents := make([][]byte, 0, len(layers))

	for _, arn := range layers {
		if c := h.layerContentFor(arn); len(c) > 0 {
			contents = append(contents, c)
		}
	}

	if len(contents) == 0 {
		return code, nil
	}

	return mergeZips(code, contents)
}

// mergeZips overlays each layer zip's entries onto the function zip, returning a
// new zip. The first writer of a given path wins, so function entries take
// precedence over layers and earlier layers over later ones.
func mergeZips(code []byte, layers [][]byte) ([]byte, error) {
	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)
	seen := make(map[string]bool)

	if err := copyZipInto(zw, code, seen, false); err != nil {
		return nil, err
	}

	for _, layer := range layers {
		if err := copyZipInto(zw, layer, seen, true); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "finalize deployment package: %v", err)
	}

	return buf.Bytes(), nil
}

// copyZipInto copies every file entry of src into zw, skipping directories and
// paths already written. When stripPrefix is set, a runtime-specific layer root
// is trimmed so the file lands where imports resolve.
func copyZipInto(zw *zip.Writer, src []byte, seen map[string]bool, stripPrefix bool) error {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "read deployment package: %v", err)
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		name := f.Name
		if stripPrefix {
			name = stripLayerPrefix(name)
		}

		if name == "" || seen[name] {
			continue
		}

		seen[name] = true

		if err := copyZipFile(zw, f, name); err != nil {
			return err
		}
	}

	return nil
}

// copyZipFile writes one archive entry into zw under name.
func copyZipFile(zw *zip.Writer, f *zip.File, name string) error {
	rc, err := f.Open()
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "open archive entry %q: %v", f.Name, err)
	}

	defer func() { _ = rc.Close() }()

	dst, err := zw.Create(name)
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "write archive entry %q: %v", name, err)
	}

	if _, err := io.Copy(dst, io.LimitReader(rc, maxLayerFileBytes)); err != nil {
		return cerrors.Newf(cerrors.Internal, "copy archive entry %q: %v", name, err)
	}

	return nil
}

// stripLayerPrefix trims a runtime-specific layer root so overlaid modules land
// alongside the function code where the subprocess engine can import them.
// Only the language root is stripped — NOT "nodejs/node_modules/": a real Node
// layer entry "nodejs/node_modules/express/index.js" must land at
// "node_modules/express/index.js" so Node's own resolver finds it from the
// function dir (the engine sets cmd.Dir but no NODE_PATH). Python "python/…"
// lands at the function-dir root, which is already on sys.path.
func stripLayerPrefix(name string) string {
	for _, p := range []string{"python/", "nodejs/"} {
		if strings.HasPrefix(name, p) {
			return strings.TrimPrefix(name, p)
		}
	}

	return name
}

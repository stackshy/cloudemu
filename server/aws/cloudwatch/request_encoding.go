package cloudwatch

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

// PutMetricData carries the requestCompression trait, so the SDKs gzip its
// body once it passes 10 KiB and send Content-Encoding: gzip. That happens
// on every protocol, so the body is unwrapped before any codec reads it.
func decodeRequestBody(r *http.Request) {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		return
	}

	r.Body = &gzipBody{src: r.Body}
	r.Header.Del("Content-Encoding")
	r.ContentLength = -1
}

// gzipBody opens the gzip stream on the first Read, so a bad stream shows up
// as a read error in the codec that reads the body, which answers it in its
// own wire format.
type gzipBody struct {
	src io.ReadCloser
	zr  *gzip.Reader
}

func (g *gzipBody) Read(p []byte) (int, error) {
	if g.zr == nil {
		zr, err := gzip.NewReader(g.src)
		if err != nil {
			return 0, err
		}

		g.zr = zr
	}

	return g.zr.Read(p)
}

func (g *gzipBody) Close() error {
	return g.src.Close()
}

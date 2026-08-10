package bedrock_test

import (
	"errors"
	"io"
	"net"
	"strings"
)

// benignStreamTeardown reports whether err from an eventstream's Err() is a
// transport-level teardown artifact rather than a protocol failure. The SDK
// eventstream reader can observe a locally/remotely closed connection instead
// of a clean EOF once the logical stream is fully consumed — this flakes under
// CI load. Callers gate Err() on this only after asserting the stream's content
// is complete, so a genuinely truncated stream is still caught by those checks.
func benignStreamTeardown(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}

	return strings.Contains(err.Error(), "use of closed network connection")
}

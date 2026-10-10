// metadata_case_test.go: checks the raw case of the x-amz-meta-* response
// headers. Go's HTTP client canonicalizes header names, so this reads the
// response bytes off a raw connection, the way botocore sees them.
package s3_test

import (
	"bufio"
	"fmt"
	"net"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// rawRequest sends req on a fresh connection to addr and returns the response
// header lines exactly as written by the server.
func rawRequest(t *testing.T, addr, req string) []string {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprint(conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	tp := textproto.NewReader(bufio.NewReader(conn))

	status, err := tp.ReadLine()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}

	lines := []string{status}

	for {
		line, err := tp.ReadLine()
		if err != nil {
			t.Fatalf("read header: %v", err)
		}

		if line == "" {
			return lines
		}

		lines = append(lines, line)
	}
}

func TestS3UserMetadataHeadersAreLowercase(t *testing.T) {
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{S3: cloudemu.NewAWS().S3}))
	t.Cleanup(ts.Close)

	addr := strings.TrimPrefix(ts.URL, "http://")
	host := "Host: " + addr + "\r\n"

	rawRequest(t, addr, "PUT /metacase HTTP/1.1\r\n"+host+"Content-Length: 0\r\nConnection: close\r\n\r\n")
	rawRequest(t, addr, "PUT /metacase/obj HTTP/1.1\r\n"+host+
		"x-amz-meta-Foo-Bar: baz\r\nContent-Length: 1\r\nConnection: close\r\n\r\nx")

	for _, method := range []string{"GET", "HEAD"} {
		lines := rawRequest(t, addr, method+" /metacase/obj HTTP/1.1\r\n"+host+"Connection: close\r\n\r\n")

		if !strings.Contains(lines[0], " 200 ") {
			t.Fatalf("%s status line = %q, want 200", method, lines[0])
		}

		found := false

		for _, l := range lines[1:] {
			name, value, _ := strings.Cut(l, ":")
			if !strings.EqualFold(name, "x-amz-meta-foo-bar") {
				continue
			}

			found = true

			if name != "x-amz-meta-foo-bar" {
				t.Fatalf("%s metadata header name = %q, want lowercase x-amz-meta-foo-bar", method, name)
			}

			if strings.TrimSpace(value) != "baz" {
				t.Fatalf("%s metadata value = %q, want baz", method, value)
			}
		}

		if !found {
			t.Fatalf("%s response has no x-amz-meta-foo-bar header: %q", method, lines)
		}
	}
}

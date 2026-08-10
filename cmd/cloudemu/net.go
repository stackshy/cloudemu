//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/features/topology"
)

// netPositionalArgs is the number of positional args both subcommands take
// (can-connect A B; trace A destIP).
const netPositionalArgs = 2

// flagJSON requests machine-readable JSON output (net and cost commands).
const flagJSON = "--json"

var (
	errNetUsage = errors.New("usage: cloudemu net <can-connect <A> <B> [--port N] [--protocol tcp] | " +
		"trace <A> <destIP>> [--json] [--home dir]")
	errNetServer = errors.New("network query failed")
)

// parseNetFlags splits --home/--port/--protocol/--json out of args, returning
// the remaining positional args.
func parseNetFlags(args []string) (home, port, proto string, jsonOut bool, pos []string) {
	home, rest := splitHomeFlag(args)
	pos = make([]string, 0, len(rest))

	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == flagJSON:
			jsonOut = true
		case a == "--port" && i+1 < len(rest):
			port = rest[i+1]
			i++
		case strings.HasPrefix(a, "--port="):
			port = strings.TrimPrefix(a, "--port=")
		case a == "--protocol" && i+1 < len(rest):
			proto = rest[i+1]
			i++
		case strings.HasPrefix(a, "--protocol="):
			proto = strings.TrimPrefix(a, "--protocol=")
		default:
			pos = append(pos, a)
		}
	}

	return home, port, proto, jsonOut, pos
}

// runNet dispatches the net can-connect / trace subcommands.
func runNet(args []string) error {
	if len(args) == 0 {
		return errNetUsage
	}

	home, port, proto, jsonOut, pos := parseNetFlags(args[1:])
	if len(pos) != netPositionalArgs {
		return errNetUsage
	}

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	base, err := adminBaseURL(dir)
	if err != nil {
		return err
	}

	switch args[0] {
	case "can-connect":
		return netCanConnect(base, pos[0], pos[1], port, proto, jsonOut)
	case "trace":
		return netTrace(base, pos[0], pos[1], jsonOut)
	default:
		return errNetUsage
	}
}

func netCanConnect(base, from, to, port, proto string, jsonOut bool) error {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)

	if port != "" {
		q.Set("port", port)
	}

	if proto != "" {
		q.Set("protocol", proto)
	}

	body, err := netGET(base, "net/can-connect", q)
	if err != nil {
		return err
	}

	if jsonOut {
		fmt.Println(string(body))

		return nil
	}

	var res topology.ConnectivityResult
	if err := json.Unmarshal(body, &res); err != nil {
		return err
	}

	verdict := "NO"
	if res.Allowed {
		verdict = "YES"
	}

	fmt.Printf("%s — %s\n", verdict, res.Reason)
	printHops(res.Path)

	return nil
}

func netTrace(base, from, dest string, jsonOut bool) error {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", dest)

	body, err := netGET(base, "net/trace", q)
	if err != nil {
		return err
	}

	if jsonOut {
		fmt.Println(string(body))

		return nil
	}

	var out struct {
		Hops []topology.RouteHop `json:"hops"`
	}

	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}

	printHops(out.Hops)

	return nil
}

func printHops(hops []topology.RouteHop) {
	for i := range hops {
		h := &hops[i]
		line := "  → " + h.Type

		if h.ResourceID != "" {
			line += " " + h.ResourceID
		}

		if h.Detail != "" {
			line += " (" + h.Detail + ")"
		}

		fmt.Println(line)
	}
}

// netGET calls a /_cloudemu/<endpoint> control path and returns the body,
// mapping the control plane's error statuses to clear CLI errors.
func netGET(base, endpoint string, q url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapHTTPTimeout)
	defer cancel()

	u := base + "/_cloudemu/" + endpoint
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errSnapDaemonDown, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotImplemented {
		return nil, errSnapAdminOff
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", errNetServer, serverErrMsg(b, resp.Status))
	}

	return b, nil
}

// serverErrMsg extracts the {"error":...} message from a control-plane response,
// falling back to the HTTP status.
func serverErrMsg(body []byte, status string) string {
	var e struct {
		Error string `json:"error"`
	}

	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return e.Error
	}

	return status
}

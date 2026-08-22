package functions

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
)

var (
	errUnsupportedRuntime  = errors.New("unsupported runtime: real execution supports python* and nodejs*")
	errNodeHTTPUnsupported = errors.New(
		"http framework not yet supported for node runtimes: gen1 Node uses the Express (req,res) contract — follow-up",
	)
)

// frameworkHTTP is the functions-framework request/response invocation contract
// (GCP Cloud Functions gen1). The empty framework is the event contract
// fn(event, context) used by AWS Lambda / Azure Functions.
const frameworkHTTP = "http"

// runtime describes how to execute a supported language runtime family: which
// interpreter to spawn, the source-file extension for the handler, the default
// source file used for a bare (dotless) HTTP entrypoint, and the bootstrap
// script that loads the handler and runs it against the event.
type runtime struct {
	interpreter string
	ext         string
	runnerName  string
	runnerSrc   string
	defaultFile string // source file for a bare HTTP entrypoint (no file part)
}

// runtimeFor maps a Lambda-style runtime identifier (e.g. "python3.12",
// "nodejs20.x") plus the invocation framework to its execution recipe. The
// framework selects the bootstrap runner: the event contract fn(event, context)
// for "" or the functions-framework request/response contract for "http".
func runtimeFor(id, framework string) (runtime, error) {
	lower := strings.ToLower(id)

	switch {
	case strings.HasPrefix(lower, "python"):
		rt := runtime{interpreter: "python3", ext: ".py", runnerName: "_cloudemu_runner.py", defaultFile: "main"}
		if framework == frameworkHTTP {
			rt.runnerSrc = pythonHTTPRunner
		} else {
			rt.runnerSrc = pythonRunner
		}

		return rt, nil
	case strings.HasPrefix(lower, "node"):
		if framework == frameworkHTTP {
			// gen1 Node functions use the Express (req, res)=>res.json(...)
			// contract, which needs a response object the shim must fake. Left
			// as a follow-up; Python is fully supported for http today.
			return runtime{}, errNodeHTTPUnsupported
		}

		return runtime{interpreter: "node", ext: ".js", runnerName: "_cloudemu_runner.js",
			runnerSrc: nodeRunner, defaultFile: "index"}, nil
	default:
		return runtime{}, fmt.Errorf("%q: %w", id, errUnsupportedRuntime)
	}
}

// pythonRunner loads the handler module by file path, calls handler(event, ctx),
// and writes {error, payload} to the result file. The event arrives on stdin;
// the handler's return value is JSON-encoded into payload. A raised exception
// becomes error (the function-error path) rather than a runner crash.
const pythonRunner = `import sys, os, json, importlib.util

def _run():
    handler_file = os.environ["_CLOUDEMU_HANDLER_FILE"]
    func_name = os.environ["_CLOUDEMU_HANDLER_FUNC"]
    result_file = os.environ["_CLOUDEMU_RESULT_FILE"]

    try:
        raw = sys.stdin.read()
        event = json.loads(raw) if raw.strip() else None
        spec = importlib.util.spec_from_file_location("cloudemu_handler", handler_file)
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        fn = getattr(mod, func_name)
        result = fn(event, None)
        out = {"error": "", "payload": json.dumps(result)}
    except Exception as e:
        out = {"error": "%s: %s" % (type(e).__name__, e), "payload": ""}

    with open(result_file, "w") as f:
        json.dump(out, f)

_run()
`

// pythonHTTPRunner runs a gen1 Cloud Functions handler under the
// functions-framework HTTP contract: fn(request) receives a Flask-Request-like
// object built here with the stdlib only (no Flask dependency) and returns a
// Flask-coercible value (dict→JSON, str, or a (body, ...) tuple). The event
// bytes arrive on stdin as the request body; get_json() parses them, .data
// exposes the raw bytes, and .args/.headers are empty. The return value is
// JSON-encoded into payload the way Flask would coerce it; a raised exception
// becomes the function-error path.
const pythonHTTPRunner = `import sys, os, json, importlib.util

class _Request:
    def __init__(self, raw):
        self._raw = raw
        self.data = raw.encode("utf-8")
        self.args = {}
        self.headers = {}
        self.method = "POST"

    def get_json(self, silent=False, force=False):
        if not self._raw.strip():
            return None
        try:
            return json.loads(self._raw)
        except Exception:
            if silent:
                return None
            raise

def _encode(result):
    # Mirror Flask's response coercion: a tuple is (body, status[, headers]);
    # a dict/list becomes JSON; bytes/str pass through as text.
    if isinstance(result, tuple):
        result = result[0] if result else ""
    if isinstance(result, (dict, list)):
        return json.dumps(result)
    if isinstance(result, bytes):
        return result.decode("utf-8")
    return str(result)

def _run():
    handler_file = os.environ["_CLOUDEMU_HANDLER_FILE"]
    func_name = os.environ["_CLOUDEMU_HANDLER_FUNC"]
    result_file = os.environ["_CLOUDEMU_RESULT_FILE"]

    try:
        raw = sys.stdin.read()
        spec = importlib.util.spec_from_file_location("cloudemu_handler", handler_file)
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        fn = getattr(mod, func_name)
        result = fn(_Request(raw))
        if result is None:
            # Flask/gen1 treats a None return as a server error, not a 200.
            raise ValueError("function returned None (no response)")
        out = {"error": "", "payload": _encode(result)}
    except Exception as e:
        out = {"error": "%s: %s" % (type(e).__name__, e), "payload": ""}

    with open(result_file, "w") as f:
        json.dump(out, f)

_run()
`

// nodeRunner mirrors pythonRunner for Node: require the handler module, await
// handler(event, ctx), and write {error, payload}.
const nodeRunner = `const fs = require("fs");

async function run() {
  const handlerFile = process.env._CLOUDEMU_HANDLER_FILE;
  const funcName = process.env._CLOUDEMU_HANDLER_FUNC;
  const resultFile = process.env._CLOUDEMU_RESULT_FILE;

  const raw = fs.readFileSync(0, "utf8");
  const event = raw.trim() ? JSON.parse(raw) : null;

  let out;
  try {
    const mod = require(handlerFile);
    const fn = mod[funcName];
    if (typeof fn !== "function") {
      throw new Error("handler '" + funcName + "' is not an exported function");
    }
    const result = await fn(event, {});
    out = { error: "", payload: JSON.stringify(result === undefined ? null : result) };
  } catch (e) {
    const name = e && e.name ? e.name : "Error";
    const msg = e && e.message ? e.message : String(e);
    out = { error: name + ": " + msg, payload: "" };
  }

  fs.writeFileSync(resultFile, JSON.stringify(out));
}

run();
`

// staticFunctionEngineCheck asserts Subprocess satisfies the FunctionEngine
// contract at compile time.
var _ config.FunctionEngine = (*Subprocess)(nil)

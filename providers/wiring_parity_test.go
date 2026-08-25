// Package providers_test guards against the "forgotten cross-service SetX
// injector" class of bug: a service mock grows a Set<X>(...) wiring method
// (e.g. SetMonitoring, SetLambdaInvoker) but the provider factory's New()
// never calls it, so the wiring silently no-ops at runtime instead of failing
// to compile.
//
// For each of cloudemu.NewAWS() / NewAzure() / NewGCP() this file:
//  1. Reflects over every exported *Provider field to discover the
//     cross-service-wiring-shaped Set<X> methods the underlying service mock
//     exposes — a method named Set<Something> that takes exactly one
//     interface-typed parameter (SetMonitoring(mondriver.Monitoring),
//     SetLambdaInvoker(LambdaInvoker), SetNICAttacher(NICAttacher), ...).
//     This deliberately excludes plain resource-attribute setters such as
//     SetAlarmState(ctx, ...) or SetBucketVersioning(ctx, ...): those take a
//     context.Context and/or concrete scalar/struct arguments, not a single
//     injected interface, so they are not "wiring" in the sense this test
//     cares about.
//  2. Parses the provider's own New() source with go/ast and collects every
//     `<recv>.<Field>.<Method>(...)` call it actually makes. Parsing the real
//     source (rather than hand-maintaining a registry that can drift) is the
//     "lightweight, robust, low-maintenance" cross-check: it can never go
//     stale, because it *is* New().
//  3. Fails if a discovered Set<X> method for a field was never called in
//     New() — the exact silent-omission bug audit findings #2/#10 describe —
//     unless the (field, method) pair is in a short, commented exemption
//     list for a documented, deliberate non-wiring.
//
// # Cross-provider event-wiring parity
//
// A small orientation table for the event-delivery paths this test's AWS
// registry exercises, and their Azure/GCP equivalents in the mocks today:
//
//	Path                              AWS                                    Azure                                   GCP
//	--------------------------------  -------------------------------------  --------------------------------------  --------------------------------------
//	Object storage write -> function  S3.SetLambdaInvoker (wired)            N/A: blobstorage/eventgrid expose no    N/A: gcs/eventarc expose no
//	                                                                         invoker injector (Blob->EventGrid->     invoker injector (GCS->Eventarc->
//	                                                                         Functions is not modeled)               CloudFunctions is not modeled)
//	Change stream -> function         DynamoDB.SetStreamInvoker (wired)      N/A: cosmosdb records its change feed   N/A: firestore records its change
//	                                                                         internally but exposes no invoker       stream internally but exposes no
//	                                                                         injector                                invoker injector
//	Event bus -> targets              EventBridge.Set{SQSDeliverer,          N/A: eventgrid exposes SetMonitoring    N/A: eventarc exposes SetMonitoring
//	                                  LambdaInvoker,SNSPublisher,            only, no target-delivery injector       only, no target-delivery injector
//	                                  StepFunctionsStarter} (wired)
//	Topic -> queue fan-out            SNS.SetSQSDeliverer (wired)            N/A: servicebus exposes no fan-out      N/A: pubsub exposes no fan-out
//	                                                                         injector (SetTrigger is a per-queue     injector (SetTrigger is a per-queue
//	                                                                         callback registration, not a           callback registration, not a
//	                                                                         provider-wired interface)               provider-wired interface)
//	Alarm -> notification             CloudWatch.SetSNSPublisher (wired)     N/A: monitor exposes no alert-action    N/A: monitoring exposes no
//	                                                                         injector                                alert-action injector
//	Queue -> function invocation      SQS.SetEventSourceInvoker (wired)      N/A: no invoker injector exposed        N/A: no invoker injector exposed
//
// "N/A" above means the mock exposes no Set<X> injector for that path today
// (so this test has nothing to require there), not that real Azure/GCP lack
// the capability — it is a statement about mock coverage, not the real
// clouds. Where a provider does expose an injector, TestWiringParity_*
// enforces New() actually wires it.
package providers_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/providers/azure"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
)

// setXPattern matches the cross-service-wiring method-name shape: Set
// followed by an upper-case-led identifier (SetMonitoring, SetLambdaInvoker,
// ...). A bare "Set" (e.g. the cache mocks' Set(ctx, key, value, ttl)) does
// not match.
var setXPattern = regexp.MustCompile(`^Set[A-Z]`)

// fieldMethod identifies one Provider field's one Set<X> method, e.g.
// {"EC2", "SetMonitoring"}.
type fieldMethod struct {
	field  string
	method string
}

// exemption documents one (field, method) pair that this test's reflection
// pass discovers as an exposed wiring-shaped injector, but that the provider
// factory deliberately does not call — with the reason on record so it reads
// as a decision, not an oversight.
type exemption struct {
	field, method, reason string
}

// azureExemptions lists Azure's one deliberate non-wiring.
var azureExemptions = []exemption{
	{
		field:  "QueueStorage",
		method: "SetMonitoring",
		reason: "QueueStorage reuses servicebus.Mock for the Azure Queue Storage " +
			"data-plane, but that Mock's metric emission is hardcoded to the " +
			"namespace \"Microsoft.ServiceBus/namespaces\" (see servicebus.go). " +
			"Wiring it here would tag Queue Storage metrics under the wrong " +
			"Azure Monitor namespace, so ServiceBus (the real Service Bus " +
			"instance) is wired and QueueStorage intentionally is not.",
	},
}

// exposedInjectors returns, sorted, the names of v's methods shaped like a
// cross-service-wiring injector: Set<X> taking exactly one interface-typed
// parameter. v must be the reflect.Value of an exported *Provider field.
func exposedInjectors(v reflect.Value) []string {
	t := v.Type()

	var out []string

	for i := range t.NumMethod() {
		m := t.Method(i)
		if !setXPattern.MatchString(m.Name) {
			continue
		}

		mt := v.MethodByName(m.Name).Type()
		if mt.NumIn() == 1 && mt.In(0).Kind() == reflect.Interface {
			out = append(out, m.Name)
		}
	}

	return out
}

// parseWiredSetters parses the New() function in the given source file and
// returns every (field, method) pair actually called there as
// `<recv>.<field>.<method>(...)`, where <recv> is whatever local variable
// New() assigns `&Provider{...}` to (normally "p", but this discovers it
// rather than assuming it, so a rename doesn't silently defeat the check).
func parseWiredSetters(t *testing.T, path string) map[fieldMethod]bool {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var newFn *ast.FuncDecl

	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "New" {
			newFn = fd
			break
		}
	}

	if newFn == nil {
		t.Fatalf("%s: no top-level func New() found", path)
	}

	recv := providerReceiverName(newFn)
	if recv == "" {
		t.Fatalf("%s: New() has no `<var> := &Provider{...}` assignment to identify its receiver variable", path)
	}

	wired := map[fieldMethod]bool{}

	ast.Inspect(newFn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(sel.Sel.Name, "Set") {
			return true
		}

		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		base, ok := inner.X.(*ast.Ident)
		if !ok || base.Name != recv {
			return true
		}

		wired[fieldMethod{field: inner.Sel.Name, method: sel.Sel.Name}] = true

		return true
	})

	return wired
}

// providerReceiverName finds the local variable New() assigns `&Provider{}`
// to, e.g. `p` in `p := &Provider{...}`.
func providerReceiverName(newFn *ast.FuncDecl) string {
	recv := ""

	ast.Inspect(newFn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}

		lhs, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}

		unary, ok := as.Rhs[0].(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			return true
		}

		cl, ok := unary.X.(*ast.CompositeLit)
		if !ok {
			return true
		}

		if id, ok := cl.Type.(*ast.Ident); ok && id.Name == "Provider" {
			recv = lhs.Name
		}

		return true
	})

	return recv
}

// thisDir returns the directory containing this test file, so the New()
// source files can be located relative to it regardless of the test
// runner's working directory.
func thisDir(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	return filepath.Dir(file)
}

// checkWiringParity is the shared body of the per-provider tests: it
// reflects over every exported field of providerPtr's underlying struct,
// checks each discovered wiring-shaped Set<X> method against what newFile's
// New() actually calls, and fails on any mismatch.
func checkWiringParity(t *testing.T, providerPtr any, newFile string, exemptions []exemption) {
	t.Helper()

	wired := parseWiredSetters(t, filepath.Join(thisDir(t), newFile))

	exempt := map[fieldMethod]string{}
	for _, e := range exemptions {
		exempt[fieldMethod{field: e.field, method: e.method}] = e.reason
	}

	used := map[fieldMethod]bool{}

	v := reflect.ValueOf(providerPtr).Elem()
	pt := v.Type()

	for i := range pt.NumField() {
		sf := pt.Field(i)
		if !sf.IsExported() || sf.Type.Kind() != reflect.Ptr {
			continue
		}

		fv := v.Field(i)

		for _, method := range exposedInjectors(fv) {
			key := fieldMethod{field: sf.Name, method: method}

			switch {
			case wired[key]:
				if _, isExempt := exempt[key]; isExempt {
					t.Errorf(
						"%s.%s.%s is wired in New() — remove the now-stale exemption for it",
						pt.Name(), sf.Name, method,
					)
				}
			case exempt[key] != "":
				used[key] = true
			default:
				t.Errorf(
					"%s exposes %s.%s but New() (%s) never calls it — either wire it "+
						"in New(), or add a commented exemption if this is deliberate",
					pt.Name(), sf.Name, method, newFile,
				)
			}
		}
	}

	for _, e := range exemptions {
		key := fieldMethod{field: e.field, method: e.method}
		if !used[key] {
			t.Errorf(
				"exemption for %s.%s is unused — the method no longer exists (or is no "+
					"longer this wiring shape), remove the stale exemption entry",
				e.field, e.method,
			)
		}
	}
}

func TestWiringParity_AWS(t *testing.T) {
	checkWiringParity(t, aws.New(), filepath.Join("aws", "aws.go"), nil)
}

func TestWiringParity_Azure(t *testing.T) {
	checkWiringParity(t, azure.New(), filepath.Join("azure", "azure.go"), azureExemptions)
}

func TestWiringParity_GCP(t *testing.T) {
	checkWiringParity(t, gcp.New(), filepath.Join("gcp", "gcp.go"), nil)
}

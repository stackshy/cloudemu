package cloudformation

import (
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ResolvedResource is the outcome of provisioning one resource, as the resolver
// needs it: the value Ref returns for the resource, plus the attributes
// Fn::GetAtt can read.
type ResolvedResource struct {
	RefValue   string
	Attributes map[string]string
}

// Resolver evaluates the common CloudFormation intrinsic functions against the
// parameters and already-provisioned resources of a stack. It is created once
// per stack operation and consulted as each resource (in dependency order) has
// its properties resolved.
type Resolver struct {
	Params    map[string]string
	Resources map[string]ResolvedResource
	Region    string
	AccountID string
	StackName string
	StackID   string
}

// Intrinsic function names supported by the resolver.
const (
	fnRef    = "Ref"
	fnGetAtt = "Fn::GetAtt"
	fnSub    = "Fn::Sub"
	fnJoin   = "Fn::Join"
)

var subVarPattern = regexp.MustCompile(`\$\{[^}]+\}`)

// Resolve walks a decoded JSON value, replacing any intrinsic-function object
// with its evaluated result and recursing through plain maps and lists. The
// returned tree carries plain scalars/maps/lists a provisioner can read.
func (r *Resolver) Resolve(node any) (any, error) {
	switch v := node.(type) {
	case map[string]any:
		if fn, arg, ok := intrinsic(v); ok {
			return r.evalIntrinsic(fn, arg)
		}

		return r.resolveMap(v)
	case []any:
		return r.resolveList(v)
	default:
		return node, nil
	}
}

// ResolveString resolves a node and coerces the result to a string, the form a
// Ref/GetAtt/Sub/Join used as an output value or scalar property carries.
func (r *Resolver) ResolveString(node any) (string, error) {
	v, err := r.Resolve(node)
	if err != nil {
		return "", err
	}

	return scalarString(v), nil
}

func (r *Resolver) resolveMap(m map[string]any) (any, error) {
	out := make(map[string]any, len(m))

	for k, v := range m {
		rv, err := r.Resolve(v)
		if err != nil {
			return nil, err
		}

		out[k] = rv
	}

	return out, nil
}

func (r *Resolver) resolveList(l []any) (any, error) {
	out := make([]any, 0, len(l))

	for _, e := range l {
		rv, err := r.Resolve(e)
		if err != nil {
			return nil, err
		}

		out = append(out, rv)
	}

	return out, nil
}

// intrinsic reports whether m is a single-key intrinsic-function object and
// returns the function name and its argument.
func intrinsic(m map[string]any) (fn string, arg any, ok bool) {
	if len(m) != 1 {
		return "", nil, false
	}

	for k, v := range m {
		if k == fnRef || strings.HasPrefix(k, "Fn::") {
			return k, v, true
		}
	}

	return "", nil, false
}

func (r *Resolver) evalIntrinsic(fn string, arg any) (any, error) {
	switch fn {
	case fnRef:
		return r.ref(scalarString(arg))
	case fnGetAtt:
		return r.getAtt(arg)
	case fnSub:
		return r.sub(arg)
	case fnJoin:
		return r.join(arg)
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported intrinsic function %q", fn)
	}
}

// ref resolves Ref: a pseudo-parameter, a template parameter, or a resource
// (returning that resource's Ref value — its physical id or ARN).
func (r *Resolver) ref(name string) (string, error) {
	if v, ok := r.pseudo(name); ok {
		return v, nil
	}

	if v, ok := r.Params[name]; ok {
		return v, nil
	}

	if res, ok := r.Resources[name]; ok {
		return res.RefValue, nil
	}

	return "", cerrors.Newf(cerrors.InvalidArgument, "unresolved Ref to %q", name)
}

func (r *Resolver) pseudo(name string) (string, bool) {
	switch name {
	case "AWS::Region":
		return r.Region, true
	case "AWS::AccountId":
		return r.AccountID, true
	case "AWS::StackName":
		return r.StackName, true
	case "AWS::StackId":
		return r.StackID, true
	case "AWS::Partition":
		return "aws", true
	case "AWS::URLSuffix":
		return "amazonaws.com", true
	case "AWS::NoValue":
		return "", true
	default:
		return "", false
	}
}

// getAtt resolves Fn::GetAtt, accepting either ["Logical","Attr"] or the
// "Logical.Attr" string short form.
func (r *Resolver) getAtt(arg any) (string, error) {
	logical, attr := splitGetAtt(arg)
	if logical == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "malformed Fn::GetAtt")
	}

	res, ok := r.Resources[logical]
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument, "Fn::GetAtt references unknown resource %q", logical)
	}

	val, ok := res.Attributes[attr]
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"attribute %q is not available on resource %q", attr, logical)
	}

	return val, nil
}

func splitGetAtt(arg any) (logical, attr string) {
	switch v := arg.(type) {
	case []any:
		if len(v) >= 2 { //nolint:mnd // GetAtt is [logicalId, attribute]
			logical = scalarString(v[0])
			attr = scalarString(v[1])

			for _, extra := range v[2:] {
				attr += "." + scalarString(extra)
			}
		}
	case string:
		if i := strings.Index(v, "."); i >= 0 {
			logical, attr = v[:i], v[i+1:]
		}
	}

	return logical, attr
}

// sub resolves Fn::Sub in both forms: a bare string, or [template, {vars}].
func (r *Resolver) sub(arg any) (string, error) {
	tmpl, locals, err := r.subParts(arg)
	if err != nil {
		return "", err
	}

	var subErr error

	out := subVarPattern.ReplaceAllStringFunc(tmpl, func(tok string) string {
		name := tok[2 : len(tok)-1] // strip ${ and }
		if strings.HasPrefix(name, "!") {
			return "${" + name[1:] + "}" // ${!Literal} -> ${Literal}
		}

		val, e := r.subVar(name, locals)
		if e != nil && subErr == nil {
			subErr = e
		}

		return val
	})

	if subErr != nil {
		return "", subErr
	}

	return out, nil
}

func (r *Resolver) subParts(arg any) (tmpl string, locals map[string]string, err error) {
	switch v := arg.(type) {
	case string:
		return v, nil, nil
	case []any:
		if len(v) == 0 {
			return "", nil, cerrors.New(cerrors.InvalidArgument, "malformed Fn::Sub")
		}

		locals = map[string]string{}

		if len(v) >= 2 { //nolint:mnd // Sub is [template, {vars}]
			m, _ := v[1].(map[string]any)
			for k, raw := range m {
				s, e := r.ResolveString(raw)
				if e != nil {
					return "", nil, e
				}

				locals[k] = s
			}
		}

		return scalarString(v[0]), locals, nil
	default:
		return "", nil, cerrors.New(cerrors.InvalidArgument, "malformed Fn::Sub")
	}
}

// subVar resolves a single ${...} token: a Sub-local variable, a
// "Logical.Attr" GetAtt, or a Ref (pseudo/parameter/resource).
func (r *Resolver) subVar(name string, locals map[string]string) (string, error) {
	if v, ok := locals[name]; ok {
		return v, nil
	}

	if i := strings.Index(name, "."); i >= 0 {
		return r.getAtt([]any{name[:i], name[i+1:]})
	}

	return r.ref(name)
}

// join resolves Fn::Join: [delimiter, [values...]].
func (r *Resolver) join(arg any) (string, error) {
	const joinArgs = 2

	parts, ok := arg.([]any)
	if !ok || len(parts) != joinArgs {
		return "", cerrors.New(cerrors.InvalidArgument, "malformed Fn::Join")
	}

	delim := scalarString(parts[0])

	list, ok := parts[1].([]any)
	if !ok {
		return "", cerrors.New(cerrors.InvalidArgument, "Fn::Join second argument must be a list")
	}

	pieces := make([]string, 0, len(list))

	for _, e := range list {
		s, err := r.ResolveString(e)
		if err != nil {
			return "", err
		}

		pieces = append(pieces, s)
	}

	return strings.Join(pieces, delim), nil
}

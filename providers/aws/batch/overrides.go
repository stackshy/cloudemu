package batch

import (
	"encoding/json"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// jsonObject is a decoded JSON object whose members stay raw, so fields the
// emulator doesn't interpret are echoed byte for byte.
type jsonObject = map[string]json.RawMessage

// jobProperties is the node/ECS/EKS detail of a new job: the job definition's
// blobs with the SubmitJob overrides applied.
type jobProperties struct {
	node, ecs, eks json.RawMessage
}

// applyPropertyOverrides applies SubmitJob nodeOverrides, ecsPropertiesOverride
// and eksPropertiesOverride to the job definition. Each override is only valid
// for a job definition that has the matching properties (SubmitJob API
// reference): ECS overrides need ecsProperties, EKS overrides eksProperties,
// and node overrides a multinode job definition.
func applyPropertyOverrides(in *driver.SubmitJobInput, jd *driver.JobDefinition) (jobProperties, error) {
	p := jobProperties{node: copyRaw(jd.NodeProperties), ecs: copyRaw(jd.EcsProperties), eks: copyRaw(jd.EksProperties)}

	var err error

	if isSet(in.EcsPropertiesOverride) {
		if !isSet(jd.EcsProperties) {
			return p, cerrors.New(cerrors.InvalidArgument,
				"ecsPropertiesOverride can only be specified for a job definition with ecsProperties")
		}

		if p.ecs, err = mergeEcsProperties(jd.EcsProperties, in.EcsPropertiesOverride); err != nil {
			return p, err
		}
	}

	if isSet(in.EksPropertiesOverride) {
		if !isSet(jd.EksProperties) {
			return p, cerrors.New(cerrors.InvalidArgument,
				"eksPropertiesOverride can only be specified for a job definition with eksProperties")
		}

		if p.eks, err = mergeEksProperties(jd.EksProperties, in.EksPropertiesOverride); err != nil {
			return p, err
		}
	}

	if isSet(in.NodeOverrides) {
		if !isSet(jd.NodeProperties) {
			return p, cerrors.New(cerrors.InvalidArgument,
				"nodeOverrides can only be specified for a multinode job definition")
		}

		if p.node, err = mergeNodeProperties(jd.NodeProperties, in.NodeOverrides); err != nil {
			return p, err
		}
	}

	return p, nil
}

// decodeObject decodes a JSON object; an unset value is an empty object.
func decodeObject(raw json.RawMessage, what string) (jsonObject, error) {
	obj := jsonObject{}
	if !isSet(raw) {
		return obj, nil
	}

	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid %s: %v", what, err)
	}

	return obj, nil
}

// decodeObjects decodes a JSON array of objects; an unset value is empty.
func decodeObjects(raw json.RawMessage, what string) ([]jsonObject, error) {
	if !isSet(raw) {
		return nil, nil
	}

	var out []jsonObject
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid %s: %v", what, err)
	}

	return out, nil
}

func encodeJSON(v any, what string) (json.RawMessage, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "encode %s: %v", what, err)
	}

	return out, nil
}

// mergeFields applies the set members of over onto base: members named in
// keyed are arrays of objects merged by the given key field (mergeKeyed),
// members in maps are string maps merged key by key, and every other member
// replaces the base value.
func mergeFields(base, over jsonObject, keyed map[string]string, maps map[string]bool) {
	for k, v := range over {
		if !isSet(v) {
			continue
		}

		switch {
		case keyed[k] != "":
			base[k] = mergeKeyed(base[k], v, keyed[k])
		case maps[k]:
			base[k] = mergeStringMap(base[k], v)
		default:
			base[k] = v
		}
	}
}

// mergeStringMap merges two JSON string maps, over winning on a shared key.
func mergeStringMap(base, over json.RawMessage) json.RawMessage {
	var b, o map[string]json.RawMessage

	if err := json.Unmarshal(over, &o); err != nil {
		return over
	}

	if isSet(base) {
		if err := json.Unmarshal(base, &b); err != nil {
			return over
		}
	}

	if b == nil {
		b = make(map[string]json.RawMessage, len(o))
	}

	for k, v := range o {
		b[k] = v
	}

	out, err := json.Marshal(b)
	if err != nil {
		return over
	}

	return out
}

// containerName returns the "name" member of a container object.
func containerName(c jsonObject) string {
	var s string

	if raw, ok := c["name"]; ok {
		_ = json.Unmarshal(raw, &s)
	}

	return s
}

// mergeNamedContainers applies container overrides to a container list. Each
// override names the container it replaces settings of; an unnamed override is
// accepted only when the list has exactly one container.
func mergeNamedContainers(
	base []jsonObject, overrides []jsonObject, what string, keyed map[string]string,
) ([]jsonObject, error) {
	for _, o := range overrides {
		name := containerName(o)

		target := -1

		switch {
		case name == "" && len(base) == 1:
			target = 0
		case name == "":
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"%s: name is required when the job definition has more than one container", what)
		default:
			for i := range base {
				if containerName(base[i]) == name {
					target = i

					break
				}
			}
		}

		if target < 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"%s: container %q is not defined in the job definition", what, name)
		}

		mergeFields(base[target], o, keyed, nil)
	}

	return base, nil
}

//nolint:gochecknoglobals // immutable merge rules, read-only after init
var (
	// ECS TaskContainerOverrides: environment merges by name, resource
	// requirements by type; command (and name) replace.
	ecsContainerKeyed = map[string]string{"environment": "name", "resourceRequirements": "type"}
	// EksContainerOverride: env merges by name; image, command, args and
	// resources replace.
	eksContainerKeyed = map[string]string{"env": "name"}
	// EksMetadata: labels and annotations merge key by key; namespace replaces.
	eksMetadataMaps = map[string]bool{"labels": true, "annotations": true}
)

// mergeEcsProperties applies an EcsPropertiesOverride: taskProperties[i]
// overrides the definition's taskProperties[i], each container override
// targeting a container by name.
func mergeEcsProperties(props, override json.RawMessage) (json.RawMessage, error) {
	const what = "ecsPropertiesOverride"

	base, err := decodeObject(props, "ecsProperties")
	if err != nil {
		return copyRaw(props), nil //nolint:nilerr // a non-object definition blob is echoed unchanged
	}

	over, err := decodeObject(override, what)
	if err != nil {
		return nil, err
	}

	baseTasks, err := decodeObjects(base["taskProperties"], "ecsProperties.taskProperties")
	if err != nil {
		return copyRaw(props), nil //nolint:nilerr // a non-object definition blob is echoed unchanged
	}

	overTasks, err := decodeObjects(over["taskProperties"], what+".taskProperties")
	if err != nil {
		return nil, err
	}

	if len(overTasks) > len(baseTasks) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"%s lists %d taskProperties but the job definition has %d", what, len(overTasks), len(baseTasks))
	}

	for i, ot := range overTasks {
		if mergeErr := mergeTaskContainers(baseTasks[i], ot, "containers", what, ecsContainerKeyed); mergeErr != nil {
			return nil, mergeErr
		}
	}

	if len(overTasks) > 0 {
		if base["taskProperties"], err = encodeJSON(baseTasks, "ecsProperties"); err != nil {
			return nil, err
		}
	}

	return encodeJSON(base, "ecsProperties")
}

// mergeTaskContainers merges the container overrides listed under field of
// over into the same field of base.
func mergeTaskContainers(base, over jsonObject, field, what string, keyed map[string]string) error {
	overs, err := decodeObjects(over[field], what+"."+field)
	if err != nil {
		return err
	}

	if len(overs) == 0 {
		return nil
	}

	containers, err := decodeObjects(base[field], field)
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "%s: the job definition's %s can't be overridden", what, field)
	}

	merged, err := mergeNamedContainers(containers, overs, what+"."+field, keyed)
	if err != nil {
		return err
	}

	base[field], err = encodeJSON(merged, field)

	return err
}

// mergeEksProperties applies an EksPropertiesOverride to the definition's
// podProperties: containers and initContainers by name, plus pod metadata.
func mergeEksProperties(props, override json.RawMessage) (json.RawMessage, error) {
	const what = "eksPropertiesOverride"

	base, err := decodeObject(props, "eksProperties")
	if err != nil {
		return copyRaw(props), nil //nolint:nilerr // a non-object definition blob is echoed unchanged
	}

	over, err := decodeObject(override, what)
	if err != nil {
		return nil, err
	}

	overPod, err := decodeObject(over["podProperties"], what+".podProperties")
	if err != nil {
		return nil, err
	}

	if len(overPod) == 0 {
		return copyRaw(props), nil
	}

	pod, err := decodeObject(base["podProperties"], "eksProperties.podProperties")
	if err != nil {
		return copyRaw(props), nil //nolint:nilerr // a non-object definition blob is echoed unchanged
	}

	if podErr := mergeEksPod(pod, overPod); podErr != nil {
		return nil, podErr
	}

	if base["podProperties"], err = encodeJSON(pod, "podProperties"); err != nil {
		return nil, err
	}

	return encodeJSON(base, "eksProperties")
}

// mergeEksPod applies an EksPodPropertiesOverride to podProperties: containers
// and initContainers by name, plus pod metadata.
func mergeEksPod(pod, overPod jsonObject) error {
	const what = "eksPropertiesOverride.podProperties"

	for _, field := range []string{"containers", "initContainers"} {
		if err := mergeTaskContainers(pod, overPod, field, what, eksContainerKeyed); err != nil {
			return err
		}
	}

	if !isSet(overPod["metadata"]) {
		return nil
	}

	meta, err := mergeEksMetadata(pod["metadata"], overPod["metadata"])
	if err != nil {
		return err
	}

	pod["metadata"] = meta

	return nil
}

// mergeEksMetadata applies an EksMetadata override: labels and annotations
// merge key by key, namespace replaces.
func mergeEksMetadata(base, override json.RawMessage) (json.RawMessage, error) {
	meta, err := decodeObject(base, "eksProperties.podProperties.metadata")
	if err != nil {
		meta = jsonObject{}
	}

	overMeta, err := decodeObject(override, "eksPropertiesOverride.podProperties.metadata")
	if err != nil {
		return nil, err
	}

	mergeFields(meta, overMeta, nil, eksMetadataMaps)

	return encodeJSON(meta, "metadata")
}

// nodeRange is a resolved targetNodes range, both ends inclusive.
type nodeRange struct{ start, end int }

// parseTargetNodes resolves a targetNodes value ("0:3", ":3", "2:", ":" or a
// single index) against the job's node count: an omitted start is 0 and an
// omitted end the highest node index.
func parseTargetNodes(s string, numNodes int) (nodeRange, bool) {
	lo, hi, isRange := strings.Cut(s, ":")
	if !isRange {
		hi = lo
	}

	r := nodeRange{start: 0, end: numNodes - 1}

	if lo != "" {
		n, err := strconv.Atoi(lo)
		if err != nil || n < 0 {
			return nodeRange{}, false
		}

		r.start = n
	}

	if hi != "" {
		n, err := strconv.Atoi(hi)
		if err != nil || n < 0 {
			return nodeRange{}, false
		}

		r.end = n
	}

	return r, r.start <= r.end
}

// openUpper reports whether a targetNodes value has no upper bound ("n:" or
// ":"), and returns its lower bound.
func openUpper(s string) (lower int, open bool) {
	lo, hi, isRange := strings.Cut(s, ":")
	if !isRange || hi != "" {
		return 0, false
	}

	if lo == "" {
		return 0, true
	}

	n, err := strconv.Atoi(lo)
	if err != nil {
		return 0, false
	}

	return n, true
}

// nodeOverrides is the NodeOverrides shape; each property override stays raw
// apart from its target range.
type nodeOverrides struct {
	NumNodes              *int         `json:"numNodes"`
	NodePropertyOverrides []jsonObject `json:"nodePropertyOverrides"`
}

// nodeDefinition is the part of NodeProperties a node override is checked
// against.
type nodeDefinition struct {
	NumNodes            int          `json:"numNodes"`
	MainNode            int          `json:"mainNode"`
	NodeRangeProperties []jsonObject `json:"nodeRangeProperties"`
}

// mergeNodeProperties applies NodeOverrides: numNodes replaces the definition's
// node count under the documented conditions, and each nodePropertyOverride
// applies to every node range of the definition that lies inside its
// targetNodes.
func mergeNodeProperties(props, override json.RawMessage) (json.RawMessage, error) {
	const what = "nodeOverrides"

	var no nodeOverrides
	if err := json.Unmarshal(override, &no); err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid %s: %v", what, err)
	}

	base, err := decodeObject(props, "nodeProperties")
	if err != nil {
		return copyRaw(props), nil //nolint:nilerr // a non-object definition blob is echoed unchanged
	}

	var def nodeDefinition
	if json.Unmarshal(props, &def) != nil {
		return copyRaw(props), nil
	}

	if no.NumNodes != nil {
		if numErr := checkNumNodesOverride(&def, *no.NumNodes); numErr != nil {
			return nil, numErr
		}

		def.NumNodes = *no.NumNodes
		base["numNodes"] = json.RawMessage(strconv.Itoa(*no.NumNodes))
	}

	for i, po := range no.NodePropertyOverrides {
		if poErr := applyNodePropertyOverride(&def, po, i); poErr != nil {
			return nil, poErr
		}
	}

	if len(no.NodePropertyOverrides) > 0 {
		if base["nodeRangeProperties"], err = encodeJSON(def.NodeRangeProperties, "nodeRangeProperties"); err != nil {
			return nil, err
		}
	}

	return encodeJSON(base, "nodeProperties")
}

// checkNumNodesOverride enforces the NodeOverrides.numNodes conditions: the
// definition has a node range with an open upper boundary whose lower boundary
// is below the new count, and the main node index is below it too.
func checkNumNodesOverride(def *nodeDefinition, n int) error {
	if n < 1 {
		return cerrors.Newf(cerrors.InvalidArgument, "nodeOverrides.numNodes must be at least 1, got %d", n)
	}

	if def.MainNode >= n {
		return cerrors.Newf(cerrors.InvalidArgument,
			"nodeOverrides.numNodes (%d) must be greater than the job definition's mainNode (%d)", n, def.MainNode)
	}

	for _, r := range def.NodeRangeProperties {
		var target string
		_ = json.Unmarshal(r["targetNodes"], &target)

		if lower, open := openUpper(target); open && lower < n {
			return nil
		}
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"nodeOverrides.numNodes requires a node range with an open upper boundary (such as \"n:\") "+
			"whose lower boundary is below %d", n)
}

// nodeRangeOverrideFields says how each NodePropertyOverride member maps onto a
// NodeRangeProperty member.
//
//nolint:gochecknoglobals // immutable mapping, read-only after init
var nodeRangeOverrideFields = map[string]string{
	"containerOverrides":                   "container",
	"ecsPropertiesOverride":                "ecsProperties",
	"eksPropertiesOverride":                "eksProperties",
	"instanceTypes":                        "instanceTypes",
	"consumableResourcePropertiesOverride": "consumableResourceProperties",
}

// applyNodePropertyOverride applies one NodePropertyOverride to every node
// range of def inside its targetNodes. An override that covers no node range of
// the job definition is rejected rather than ignored.
func applyNodePropertyOverride(def *nodeDefinition, po jsonObject, i int) error {
	var target string
	_ = json.Unmarshal(po["targetNodes"], &target)

	if target == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "nodeOverrides.nodePropertyOverrides[%d].targetNodes is required", i)
	}

	or, ok := parseTargetNodes(target, def.NumNodes)
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument,
			"nodeOverrides.nodePropertyOverrides[%d].targetNodes %q is not a valid node range", i, target)
	}

	matched := false

	for _, r := range def.NodeRangeProperties {
		var rt string
		_ = json.Unmarshal(r["targetNodes"], &rt)

		rr, ok := parseTargetNodes(rt, def.NumNodes)
		if !ok || rr.start < or.start || rr.end > or.end {
			continue
		}

		if err := overrideNodeRange(r, po); err != nil {
			return err
		}

		matched = true
	}

	if !matched {
		return cerrors.Newf(cerrors.InvalidArgument,
			"nodeOverrides.nodePropertyOverrides[%d].targetNodes %q doesn't cover any node range of the job definition",
			i, target)
	}

	return nil
}

// overrideNodeRange applies one NodePropertyOverride to one node range.
func overrideNodeRange(r, po jsonObject) error {
	for from, to := range nodeRangeOverrideFields {
		v := po[from]
		if !isSet(v) {
			continue
		}

		var (
			merged json.RawMessage
			err    error
		)

		switch from {
		case "containerOverrides":
			if !isSet(r[to]) {
				return cerrors.New(cerrors.InvalidArgument,
					"nodeOverrides: containerOverrides target a node range without container properties")
			}

			merged, err = mergeContainer(r[to], v)
		case "ecsPropertiesOverride":
			merged, err = mergeEcsProperties(r[to], v)
		case "eksPropertiesOverride":
			merged, err = mergeEksProperties(r[to], v)
		default:
			merged = copyRaw(v)
		}

		if err != nil {
			return err
		}

		r[to] = merged
	}

	return nil
}

package kubernetes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Server-side Table column sets (#871). These mirror real kubectl output column
// for column; reviewers diff them against a real cluster. Priority-0 columns
// print by default, Priority-1 ("wide") only under `kubectl get -o wide`. The
// typed kinds (Pod, Deployment, Service, Namespace) resolve here via
// projectorForKind; registry-backed kinds attach their projector in
// registry_defs.go so a new kind carries its columns with it.

// Shared cell/status string literals (kept as constants to satisfy goconst and
// keep the projections consistent).
const (
	cellNone        = "<none>"
	condTypeReady   = "Ready"
	condStatusTrue  = "True"
	jobCondComplete = "Complete"
	jobCondFailed   = "Failed"
)

// projectorForKind returns the built-in Table projector for a hand-written typed
// kind (Pod, Deployment, Service, Namespace live in their own files). Registry-
// backed kinds attach their projector via resourceDef.tableColumns instead, so
// this returns nil for them and the generic NAME/AGE fallback never applies to a
// kind that actually declared columns.
func projectorForKind(kind string) *tableProjector {
	switch kind {
	case "Pod":
		return podTableProjector()
	case "Deployment":
		return deploymentTableProjector()
	case "Service":
		return serviceTableProjector()
	case "Namespace":
		return namespaceTableProjector()
	default:
		return nil
	}
}

func nameCol() metav1.TableColumnDefinition {
	return metav1.TableColumnDefinition{Name: "Name", Type: "string", Format: "name"}
}

func col(name, ctype string) metav1.TableColumnDefinition {
	return metav1.TableColumnDefinition{Name: name, Type: ctype}
}

// wideCol is a Priority-1 ("-o wide") column. Every wide column the emulator
// renders is a string, so the type is fixed.
func wideCol(name string) metav1.TableColumnDefinition {
	return metav1.TableColumnDefinition{Name: name, Type: "string", Priority: 1}
}

func ageCol() metav1.TableColumnDefinition {
	return metav1.TableColumnDefinition{Name: "Age", Type: "string"}
}

// fallbackProjector is the generic NAME/AGE table used for any kind without a
// declared projection (CRDs, rarely-listed built-ins) — so `kubectl get <x>`
// prints something sane instead of 500ing.
func fallbackProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{nameCol(), ageCol()},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{u.GetName(), age}
		},
	}
}

func podTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Ready", "string"),
			col("Status", "string"),
			col("Restarts", "string"),
			ageCol(),
			wideCol("IP"),
			wideCol("Node"),
			wideCol("Nominated Node"),
			wideCol("Readiness Gates"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{
				u.GetName(), podReadyColumn(u), computePodStatus(u), podRestarts(u), age,
				orNone(ustr(u, "status", "podIP")), orNone(ustr(u, "spec", "nodeName")),
				cellNone, cellNone,
			}
		},
	}
}

func deploymentTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Ready", "string"),
			col("Up-to-date", "integer"),
			col("Available", "integer"),
			ageCol(),
			wideCol("Containers"),
			wideCol("Images"),
			wideCol("Selector"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			ready := fmt.Sprintf("%d/%d", uint64at(u, "status", "readyReplicas"), specReplicas(u))

			return []any{
				u.GetName(), ready, uint64at(u, "status", "updatedReplicas"),
				uint64at(u, "status", "availableReplicas"), age,
				containerNames(u), containerImages(u), selectorString(u),
			}
		},
	}
}

func replicaSetTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Desired", "integer"),
			col("Current", "integer"),
			col("Ready", "integer"),
			ageCol(),
			wideCol("Containers"),
			wideCol("Images"),
			wideCol("Selector"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{
				u.GetName(), specReplicas(u), uint64at(u, "status", "replicas"),
				uint64at(u, "status", "readyReplicas"), age,
				containerNames(u), containerImages(u), selectorString(u),
			}
		},
	}
}

func statefulSetTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Ready", "string"),
			ageCol(),
			wideCol("Containers"),
			wideCol("Images"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			ready := fmt.Sprintf("%d/%d", uint64at(u, "status", "readyReplicas"), specReplicas(u))

			return []any{u.GetName(), ready, age, containerNames(u), containerImages(u)}
		},
	}
}

func daemonSetTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Desired", "integer"),
			col("Current", "integer"),
			col("Ready", "integer"),
			col("Up-to-date", "integer"),
			col("Available", "integer"),
			ageCol(),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{
				u.GetName(),
				uint64at(u, "status", "desiredNumberScheduled"),
				uint64at(u, "status", "currentNumberScheduled"),
				uint64at(u, "status", "numberReady"),
				uint64at(u, "status", "updatedNumberScheduled"),
				uint64at(u, "status", "numberAvailable"),
				age,
			}
		},
	}
}

func serviceTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Type", "string"),
			col("Cluster-IP", "string"),
			col("External-IP", "string"),
			col("Port(s)", "string"),
			ageCol(),
			wideCol("Selector"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{
				u.GetName(), orClusterIP(ustr(u, "spec", "type")), orNone(ustr(u, "spec", "clusterIP")),
				serviceExternalIP(u), servicePorts(u), age, selectorString(u),
			}
		},
	}
}

func namespaceTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Status", "string"),
			ageCol(),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{u.GetName(), ustr(u, "status", "phase"), age}
		},
	}
}

func nodeTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Status", "string"),
			col("Roles", "string"),
			ageCol(),
			col("Version", "string"),
			wideCol("Internal-IP"),
			wideCol("External-IP"),
			wideCol("OS-Image"),
			wideCol("Kernel-Version"),
			wideCol("Container-Runtime"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			return []any{
				u.GetName(), nodeStatus(u), nodeRoles(u), age, ustr(u, "status", "nodeInfo", "kubeletVersion"),
				nodeInternalIP(u), cellNone, ustr(u, "status", "nodeInfo", "osImage"),
				ustr(u, "status", "nodeInfo", "kernelVersion"), ustr(u, "status", "nodeInfo", "containerRuntimeVersion"),
			}
		},
	}
}

func jobTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Status", "string"),
			col("Completions", "string"),
			col("Duration", "string"),
			ageCol(),
			wideCol("Containers"),
			wideCol("Images"),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			completions := ustr(u, "spec", "completions")
			if completions == "" {
				completions = "1"
			}

			ratio := fmt.Sprintf("%d/%s", uint64at(u, "status", "succeeded"), completions)

			return []any{u.GetName(), jobStatus(u), ratio, jobDuration(u), age, containerNames(u), containerImages(u)}
		},
	}
}

func pvcTableProjector() *tableProjector {
	return &tableProjector{
		columns: []metav1.TableColumnDefinition{
			nameCol(),
			col("Status", "string"),
			col("Volume", "string"),
			col("Capacity", "string"),
			col("Access Modes", "string"),
			col("Storageclass", "string"),
			ageCol(),
		},
		cells: func(u *unstructured.Unstructured, age string) []any {
			capacity := ustr(u, "status", "capacity", "storage")
			if capacity == "" {
				capacity = ustr(u, "spec", "resources", "requests", "storage")
			}

			return []any{
				u.GetName(), ustr(u, "status", "phase"), ustr(u, "spec", "volumeName"),
				capacity, accessModesShort(u), ustr(u, "spec", "storageClassName"), age,
			}
		},
	}
}

// --- cell helpers -----------------------------------------------------------

// ustr reads a nested string field, "" if absent. Integer/float leaves are
// rendered decimally so a numeric field read as a string still prints.
func ustr(u *unstructured.Unstructured, path ...string) string {
	v, found, err := unstructured.NestedFieldNoCopy(u.Object, path...)
	if err != nil || !found || v == nil {
		return ""
	}

	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

// uint64at reads a nested integer field (0 if absent), tolerating a float64
// leaf (encoding/json without the unstructured scheme yields float64).
func uint64at(u *unstructured.Unstructured, path ...string) int64 {
	v, found, err := unstructured.NestedFieldNoCopy(u.Object, path...)
	if err != nil || !found {
		return 0
	}

	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	default:
		return 0
	}
}

// specReplicas is spec.replicas defaulted to 1 (matching the reconciler).
func specReplicas(u *unstructured.Unstructured) int64 {
	v, found, err := unstructured.NestedFieldNoCopy(u.Object, "spec", "replicas")
	if err != nil || !found {
		return 1
	}

	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	default:
		return 1
	}
}

func orNone(s string) string {
	if s == "" {
		return cellNone
	}

	return s
}

func orClusterIP(s string) string {
	if s == "" {
		return "ClusterIP"
	}

	return s
}

func containersSlice(u *unstructured.Unstructured) []any {
	cs, _, _ := unstructured.NestedSlice(u.Object, "spec", "template", "spec", "containers")

	return cs
}

func containerNames(u *unstructured.Unstructured) string {
	return joinContainerField(containersSlice(u), "name")
}

func containerImages(u *unstructured.Unstructured) string {
	return joinContainerField(containersSlice(u), "image")
}

func joinContainerField(containers []any, field string) string {
	var out []string

	for _, raw := range containers {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if v, ok := c[field].(string); ok {
			out = append(out, v)
		}
	}

	return strings.Join(out, ",")
}

// selectorString renders spec.selector.matchLabels as k=v,k=v (deployment/RS).
func selectorString(u *unstructured.Unstructured) string {
	ml, _, _ := unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels")
	if len(ml) == 0 {
		return cellNone
	}

	keys := make([]string, 0, len(ml))
	for k := range ml {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(ml))
	for _, k := range keys {
		parts = append(parts, k+"="+ml[k])
	}

	return strings.Join(parts, ",")
}

// podReadyColumn is "<readyContainers>/<totalContainers>".
func podReadyColumn(u *unstructured.Unstructured) string {
	total := 0
	if cs, _, _ := unstructured.NestedSlice(u.Object, "spec", "containers"); cs != nil {
		total = len(cs)
	}

	ready := 0

	statuses, _, _ := unstructured.NestedSlice(u.Object, "status", "containerStatuses")
	for _, raw := range statuses {
		cs, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if r, ok := cs["ready"].(bool); ok && r {
			ready++
		}
	}

	return fmt.Sprintf("%d/%d", ready, total)
}

// podRestarts sums restartCount across container statuses.
func podRestarts(u *unstructured.Unstructured) string {
	var total int64

	statuses, _, _ := unstructured.NestedSlice(u.Object, "status", "containerStatuses")
	for _, raw := range statuses {
		cs, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		switch rc := cs["restartCount"].(type) {
		case int64:
			total += rc
		case float64:
			total += int64(rc)
		}
	}

	return strconv.FormatInt(total, 10)
}

// computePodStatus reproduces kubectl's computed STATUS column: Terminating when
// a deletionTimestamp is set, a waiting/terminated container reason
// (ContainerCreating / CrashLoopBackOff / Completed) when present, else the
// phase.
func computePodStatus(u *unstructured.Unstructured) string {
	if ts := u.GetDeletionTimestamp(); ts != nil {
		return "Terminating"
	}

	reason := ustr(u, "status", "phase")
	if reason == "" {
		reason = "Pending"
	}

	statuses, _, _ := unstructured.NestedSlice(u.Object, "status", "containerStatuses")
	for _, raw := range statuses {
		cs, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		state, _ := cs["state"].(map[string]any)
		if r := stateReason(state, "waiting"); r != "" {
			reason = r
		} else if r := stateReason(state, "terminated"); r != "" {
			reason = r
		}
	}

	return reason
}

func stateReason(state map[string]any, phase string) string {
	sub, ok := state[phase].(map[string]any)
	if !ok {
		return ""
	}

	r, _ := sub["reason"].(string)

	return r
}

func serviceExternalIP(u *unstructured.Unstructured) string {
	ingress, _, _ := unstructured.NestedSlice(u.Object, "status", "loadBalancer", "ingress")

	var ips []string

	for _, raw := range ingress {
		lb, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if ip, ok := lb["ip"].(string); ok && ip != "" {
			ips = append(ips, ip)
		}

		if host, ok := lb["hostname"].(string); ok && host != "" {
			ips = append(ips, host)
		}
	}

	if ext, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "externalIPs"); len(ext) > 0 {
		ips = append(ips, ext...)
	}

	if len(ips) == 0 {
		return cellNone
	}

	return strings.Join(ips, ",")
}

func servicePorts(u *unstructured.Unstructured) string {
	ports, _, _ := unstructured.NestedSlice(u.Object, "spec", "ports")

	var out []string

	for _, raw := range ports {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		port := numToString(p["port"])
		proto, _ := p["protocol"].(string)

		if proto == "" {
			proto = "TCP"
		}

		if np := numToString(p["nodePort"]); np != "" && np != "0" {
			out = append(out, fmt.Sprintf("%s:%s/%s", port, np, proto))

			continue
		}

		out = append(out, fmt.Sprintf("%s/%s", port, proto))
	}

	if len(out) == 0 {
		return cellNone
	}

	return strings.Join(out, ",")
}

func numToString(v any) string {
	switch t := v.(type) {
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case string:
		return t
	default:
		return ""
	}
}

func nodeStatus(u *unstructured.Unstructured) string {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, raw := range conds {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if t, _ := c["type"].(string); t == condTypeReady {
			if s, _ := c["status"].(string); s == condStatusTrue {
				return condTypeReady
			}

			return "NotReady"
		}
	}

	return "Unknown"
}

func nodeRoles(u *unstructured.Unstructured) string {
	labels := u.GetLabels()

	var roles []string

	for k := range labels {
		if role, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && role != "" {
			roles = append(roles, role)
		}
	}

	if len(roles) == 0 {
		return cellNone
	}

	sort.Strings(roles)

	return strings.Join(roles, ",")
}

func nodeInternalIP(u *unstructured.Unstructured) string {
	addrs, _, _ := unstructured.NestedSlice(u.Object, "status", "addresses")
	for _, raw := range addrs {
		a, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if t, _ := a["type"].(string); t == "InternalIP" {
			if ip, _ := a["address"].(string); ip != "" {
				return ip
			}
		}
	}

	return cellNone
}

func jobStatus(u *unstructured.Unstructured) string {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, raw := range conds {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		ctype, _ := c["type"].(string)

		cstatus, _ := c["status"].(string)
		if cstatus != condStatusTrue {
			continue
		}

		if ctype == jobCondComplete {
			return jobCondComplete
		}

		if ctype == jobCondFailed {
			return jobCondFailed
		}
	}

	return "Running"
}

func jobDuration(u *unstructured.Unstructured) string {
	start := ustr(u, "status", "startTime")
	end := ustr(u, "status", "completionTime")

	if start == "" || end == "" {
		return cellNone
	}

	return ""
}

func accessModesShort(u *unstructured.Unstructured) string {
	modes, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "accessModes")

	var out []string

	for _, m := range modes {
		switch m {
		case "ReadWriteOnce":
			out = append(out, "RWO")
		case "ReadOnlyMany":
			out = append(out, "ROX")
		case "ReadWriteMany":
			out = append(out, "RWX")
		case "ReadWriteOncePod":
			out = append(out, "RWOP")
		default:
			out = append(out, m)
		}
	}

	return strings.Join(out, ",")
}

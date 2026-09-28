package managedkafka

import (
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

// cloneCluster returns a deep copy of c so a stored cluster is never aliased by a
// value handed back to a caller.
func cloneCluster(c *mkdriver.Cluster) mkdriver.Cluster {
	out := *c
	out.Subnets = append([]string(nil), c.Subnets...)
	out.Labels = cloneStringMap(c.Labels)
	out.TLS = cloneTLS(c.TLS)

	return out
}

// cloneTLS deep-copies a TLS config; nil stays nil.
func cloneTLS(in *mkdriver.TLSConfig) *mkdriver.TLSConfig {
	if in == nil {
		return nil
	}

	out := *in
	out.CAPools = append([]string(nil), in.CAPools...)

	return &out
}

// cloneTopic returns a deep copy of t.
func cloneTopic(t *mkdriver.Topic) mkdriver.Topic {
	out := *t
	out.Configs = cloneStringMap(t.Configs)

	return out
}

// cloneStringMap deep-copies a string map; an empty map clones to nil.
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

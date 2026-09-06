package scheduler

import "github.com/stackshy/cloudemu/v2/services/scheduler/driver"

// cloneJob returns a deep copy of a job so the store never hands out a pointer
// into its own state (copy-on-write at the driver boundary).
func cloneJob(j *driver.Job) *driver.Job {
	cp := *j
	cp.HTTPTarget = cloneHTTPTarget(j.HTTPTarget)
	cp.PubsubTarget = clonePubsubTarget(j.PubsubTarget)
	cp.AppEngineHTTPTarget = cloneAppEngineTarget(j.AppEngineHTTPTarget)
	cp.RetryConfig = cloneRetryConfig(j.RetryConfig)

	return &cp
}

func cloneHTTPTarget(in *driver.HTTPTarget) *driver.HTTPTarget {
	if in == nil {
		return nil
	}

	out := &driver.HTTPTarget{
		URI:        in.URI,
		HTTPMethod: in.HTTPMethod,
		Headers:    cloneMap(in.Headers),
		Body:       cloneBytes(in.Body),
	}

	if in.OAuthToken != nil {
		tok := *in.OAuthToken
		out.OAuthToken = &tok
	}

	if in.OidcToken != nil {
		tok := *in.OidcToken
		out.OidcToken = &tok
	}

	return out
}

func clonePubsubTarget(in *driver.PubsubTarget) *driver.PubsubTarget {
	if in == nil {
		return nil
	}

	return &driver.PubsubTarget{
		TopicName:  in.TopicName,
		Data:       cloneBytes(in.Data),
		Attributes: cloneMap(in.Attributes),
	}
}

func cloneAppEngineTarget(in *driver.AppEngineHTTPTarget) *driver.AppEngineHTTPTarget {
	if in == nil {
		return nil
	}

	out := &driver.AppEngineHTTPTarget{
		HTTPMethod:  in.HTTPMethod,
		RelativeURI: in.RelativeURI,
		Headers:     cloneMap(in.Headers),
		Body:        cloneBytes(in.Body),
	}

	if in.AppEngineRouting != nil {
		r := *in.AppEngineRouting
		out.AppEngineRouting = &r
	}

	return out
}

func cloneRetryConfig(in *driver.RetryConfig) *driver.RetryConfig {
	if in == nil {
		return nil
	}

	cp := *in

	return &cp
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}

	out := make([]byte, len(in))
	copy(out, in)

	return out
}

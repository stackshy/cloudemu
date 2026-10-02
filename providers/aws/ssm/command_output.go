package ssm

import (
	"context"
	"path"
	"strings"
	"time"

	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

// OutputStore is the slice of the S3 mock Run Command writes output through.
type OutputStore interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error
}

// SetOutputStore wires S3 in so a command that names OutputS3BucketName gets
// its output written there. Without it the bucket is only echoed back.
func (m *Mock) SetOutputStore(s OutputStore) {
	m.outputStore = s
}

// outputFolder is the S3 folder of one plugin's output, as the SSM agent
// lays it out: <prefix>/<command id>/<instance id>/<plugin>/<step>.
func (r *commandRecord) outputFolder(instanceID string, step commandStep) string {
	plugin := strings.ReplaceAll(step.Action, ":", "")

	stepDir := step.Name
	if step.Legacy {
		stepDir = "0." + plugin
	}

	parts := []string{r.Command.CommandID, instanceID, plugin, stepDir}
	if prefix := strings.Trim(r.Command.OutputS3KeyPrefix, "/"); prefix != "" {
		parts = append([]string{prefix}, parts...)
	}

	return path.Join(parts...)
}

// outputURL is the virtual S3 URL SSM reports for an output object.
func (r *commandRecord) outputURL(key string) string {
	return "https://s3." + r.Command.OutputS3Region + ".amazonaws.com/" + r.Command.OutputS3BucketName + "/" + key
}

// outputWrite is one object waiting to be written.
type outputWrite struct {
	bucket, key string
}

// flushOutputs writes the output of every invocation that finished by now
// and has not been written yet. The objects are written after cmdMu is
// released, so an S3 notification target cannot deadlock on this mock. A
// failed write (the bucket is missing, say) is dropped: real SSM reports the
// command result regardless.
func (m *Mock) flushOutputs(ctx context.Context, now time.Time) bool {
	if m.outputStore == nil {
		return false
	}

	var writes []outputWrite

	m.cmdMu.Lock()

	for _, r := range m.commands.All() {
		if r.Command.OutputS3BucketName == "" {
			continue
		}

		for i, inv := range r.Invocations {
			if inv.OutputWritten {
				continue
			}

			s := r.invocation(i, now)
			if s.status != ssmdriver.CommandSuccess && s.details != detailsExecutionTimedOut {
				continue
			}

			inv.OutputWritten = true

			for _, step := range r.Steps {
				writes = append(writes, outputWrite{
					bucket: r.Command.OutputS3BucketName, key: r.outputFolder(inv.InstanceID, step) + "/stdout",
				})
			}
		}
	}

	m.cmdMu.Unlock()

	for _, w := range writes {
		_ = m.outputStore.PutObject(ctx, w.bucket, w.key, []byte{}, "text/plain", nil)
	}

	return len(writes) > 0
}

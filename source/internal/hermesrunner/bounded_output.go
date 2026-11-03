package hermesrunner

import "bytes"

const (
	// A 15,000-token market answer and a complete notice analysis both fit well
	// below this boundary. Keep child output bounded even when an external
	// Hermes transport ignores its requested token limit.
	maxHermesResponseBytes   = 8 << 20
	maxHermesDiagnosticBytes = 64 << 10
)

// boundedOutputBuffer accepts the complete child-process write so os/exec does
// not see a short write, but retains at most limit bytes. Callers reject an
// exceeded response instead of parsing a silently truncated JSON document.
type boundedOutputBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func newBoundedOutputBuffer(limit int) *boundedOutputBuffer {
	return &boundedOutputBuffer{limit: max(0, limit)}
}

func (b *boundedOutputBuffer) Write(payload []byte) (int, error) {
	written := len(payload)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = b.exceeded || written > 0
		return written, nil
	}
	if len(payload) > remaining {
		_, _ = b.buffer.Write(payload[:remaining])
		b.exceeded = true
		return written, nil
	}
	_, _ = b.buffer.Write(payload)
	return written, nil
}

func (b *boundedOutputBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedOutputBuffer) String() string { return b.buffer.String() }
func (b *boundedOutputBuffer) Exceeded() bool { return b.exceeded }

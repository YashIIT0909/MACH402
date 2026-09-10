package runner

import (
	"sync"
)

// logBufferLines caps how much output is retained for late subscribers. A job
// that prints without bound must not grow the agent's heap without bound.
const logBufferLines = 2000

// LogLine is one line of container output as delivered to subscribers.
type LogLine struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// logBroker fans container output out to any number of SSE subscribers while
// keeping a bounded replay buffer, so a renter who connects after the job
// started still sees what it has printed so far.
type logBroker struct {
	mu          sync.Mutex
	buffer      []LogLine
	subscribers map[chan LogLine]struct{}
	closed      bool
}

func newLogBroker() *logBroker {
	return &logBroker{subscribers: make(map[chan LogLine]struct{})}
}

// publish appends a line and delivers it to every live subscriber.
func (b *logBroker) publish(line LogLine) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}

	b.buffer = append(b.buffer, line)
	if len(b.buffer) > logBufferLines {
		b.buffer = b.buffer[len(b.buffer)-logBufferLines:]
	}

	for ch := range b.subscribers {
		// Never block the container's log reader on a slow HTTP client: drop
		// for that subscriber instead, and let it catch up from the buffer.
		select {
		case ch <- line:
		default:
		}
	}
}

// subscribe returns the backlog plus a channel of future lines. The channel is
// closed when the job's output ends. The caller must call unsubscribe.
func (b *logBroker) subscribe() ([]LogLine, chan LogLine) {
	b.mu.Lock()
	defer b.mu.Unlock()

	backlog := make([]LogLine, len(b.buffer))
	copy(backlog, b.buffer)

	ch := make(chan LogLine, 256)
	if b.closed {
		close(ch)
		return backlog, ch
	}
	b.subscribers[ch] = struct{}{}
	return backlog, ch
}

func (b *logBroker) unsubscribe(ch chan LogLine) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
}

// close ends the stream for everyone. Idempotent.
func (b *logBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = make(map[chan LogLine]struct{})
}

// snapshot returns the retained output, for `GET /v1/jobs/:id/logs` without SSE.
func (b *logBroker) snapshot() []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LogLine, len(b.buffer))
	copy(out, b.buffer)
	return out
}

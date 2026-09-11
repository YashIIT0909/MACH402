package runner

import (
	"sync"
	"time"
)

// EventKind names something worth showing an operator.
type EventKind string

const (
	// Payment events, emitted by the HTTP layer.
	EventChallenged   EventKind = "challenged"
	EventVerified     EventKind = "verified"
	EventRejected     EventKind = "rejected"
	EventSettled      EventKind = "settled"
	EventSettleFailed EventKind = "settle_failed"

	// Job events, emitted by the runner.
	EventJobStaging  EventKind = "job_staging"
	EventJobStarted  EventKind = "job_started"
	EventJobFinished EventKind = "job_finished"
	EventJobReaped   EventKind = "job_reaped"

	// Lease events. A provider watching the dashboard should be able to see
	// exactly when a stranger's shell opened on their machine and when it went
	// away again, so every transition is reported.
	EventLeaseStarted  EventKind = "lease_started"
	EventLeaseExtended EventKind = "lease_extended"
	EventLeasePaused   EventKind = "lease_paused"
	EventLeaseEnded    EventKind = "lease_ended"
)

// Event is one line for the provider's dashboard.
//
// It is a display record, not an audit record: receipts.jsonl remains the
// authoritative log of what was earned, because events are dropped rather than
// queued when a subscriber falls behind.
type Event struct {
	At          time.Time
	Kind        EventKind
	JobID       string
	LeaseID     string
	Status      Status
	Detail      string
	Payer       string
	Transaction string
	Tinybars    string
}

// eventBroker fans events out to subscribers without ever blocking the
// publisher — the same discipline as logBroker, for the same reason: a slow or
// wedged dashboard must not stall a paid job or a settlement.
type eventBroker struct {
	mu          sync.Mutex
	recent      []Event
	subscribers map[chan Event]struct{}
}

// recentEvents bounds the replay buffer a late subscriber receives.
const recentEvents = 200

func newEventBroker() *eventBroker {
	return &eventBroker{subscribers: make(map[chan Event]struct{})}
}

func (b *eventBroker) publish(event Event) {
	if event.At.IsZero() {
		event.At = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.recent = append(b.recent, event)
	if len(b.recent) > recentEvents {
		b.recent = b.recent[len(b.recent)-recentEvents:]
	}

	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (b *eventBroker) subscribe() ([]Event, chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	backlog := make([]Event, len(b.recent))
	copy(backlog, b.recent)

	ch := make(chan Event, 256)
	b.subscribers[ch] = struct{}{}
	return backlog, ch
}

func (b *eventBroker) unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
}

// Publish records an event. Safe from any goroutine.
func (r *Runner) Publish(event Event) { r.events.publish(event) }

// Subscribe returns recent events plus a channel of new ones. The caller must
// call Unsubscribe.
func (r *Runner) Subscribe() ([]Event, chan Event) { return r.events.subscribe() }

// Unsubscribe releases an event subscription.
func (r *Runner) Unsubscribe(ch chan Event) { r.events.unsubscribe(ch) }

// List snapshots every job the node knows about, newest first.
func (r *Runner) List() []State {
	r.mu.RLock()
	jobs := make([]*Job, 0, len(r.jobs))
	for _, job := range r.jobs {
		jobs = append(jobs, job)
	}
	r.mu.RUnlock()

	states := make([]State, 0, len(jobs))
	for _, job := range jobs {
		states = append(states, job.State())
	}
	return states
}

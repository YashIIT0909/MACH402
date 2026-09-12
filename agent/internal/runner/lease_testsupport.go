package runner

import (
	"math/big"
	"time"
)

func secondsDuration(seconds int64) time.Duration {
	return time.Duration(seconds) * time.Second
}

// NewMeteredLeaseForTest builds a metered session's ledger with no container
// behind it.
//
// It exists because the thing most worth testing about a metered session — that
// the meter burns what it should and that the node publishes what it owes on
// every tick — lives in package httpapi and has nothing to do with Docker.
// Without this, testing the refund trail would mean standing up a real
// container, which is exactly the kind of test that stops being run.
//
// Deliberately the only way to construct a Lease from outside this package, and
// deliberately incapable of producing a runnable one: there is no container, no
// network and no volume, so nothing here can be mistaken for a shortcut around
// StartLease.
func NewMeteredLeaseForTest(id, sessionID, payer string, pricePerSecond, credit *big.Int) *Lease {
	lease := &Lease{ID: id, status: LeaseActive}
	lease.MarkMetered(sessionID, payer, pricePerSecond, credit)
	return lease
}

// RewindMeterForTest moves a session's last-tick mark backwards, so a test can
// exercise a burn without waiting in real time.
func RewindMeterForTest(lease *Lease, seconds int64) {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.lastTickAt = lease.lastTickAt.Add(-secondsDuration(seconds))
}

// NewForTest builds a Runner with just enough wired up for its event broker to
// work — no Docker connection, no GPU detection, nothing that touches the
// host. It exists for the same reason NewMeteredLeaseForTest does: testing
// that a meter tick actually reaches the dashboard's event feed needs a
// runner to publish through, and standing up a real one means Docker, which
// is exactly the dependency this kind of test should not have.
func NewForTest() *Runner {
	return &Runner{events: newEventBroker(), leases: map[string]*Lease{}}
}


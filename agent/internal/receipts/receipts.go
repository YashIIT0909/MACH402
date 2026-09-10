// Package receipts keeps the node's append-only earnings log.
//
// The provider must be able to audit earnings without trusting our website, so
// every settlement is written here locally the moment it lands. From M4 the same
// record is also published to HCS; this file stays the local source of truth.
package receipts

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Receipt is one settled payment. Amounts are strings, in the asset's smallest
// unit, and are never converted to floating point.
type Receipt struct {
	JobID          string `json:"job_id"`
	Transaction    string `json:"transaction"`
	Payer          string `json:"payer"`
	PayTo          string `json:"pay_to"`
	AmountTinybars string `json:"amount_tinybars"`
	Asset          string `json:"asset"`
	Network        string `json:"network"`
	SettledAt      string `json:"settled_at"`
}

// Log is a concurrency-safe append-only receipt file.
type Log struct {
	mu   sync.Mutex
	path string
}

// Open returns a log that appends to path. The file is created on first write.
func Open(path string) *Log {
	return &Log{path: path}
}

// Path is the file this log appends to.
func (l *Log) Path() string { return l.path }

// Append writes one receipt and fsyncs before returning. Money that moved on
// chain must not be lost to a crash, so the cost of the sync is worth paying.
func (l *Log) Append(receipt Receipt) error {
	if receipt.SettledAt == "" {
		receipt.SettledAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", l.path, err)
	}
	defer file.Close()

	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("append to %s: %w", l.path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", l.path, err)
	}
	return nil
}

// All reads every receipt back, for the TUI and for `earnings` reporting.
// A truncated final line (a crash mid-write) is skipped rather than fatal.
func (l *Log) All() ([]Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	raw, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", l.path, err)
	}

	var out []Receipt
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var receipt Receipt
		if err := json.Unmarshal(line, &receipt); err != nil {
			continue
		}
		out = append(out, receipt)
	}
	return out, nil
}

func splitLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			lines = append(lines, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

package runner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
)

// testImage is on the default allowlist and small enough to pull in CI.
const testImage = "python:3.11-slim"

// newTestRunner builds a Runner against the real Docker daemon, skipping the
// test when there is not one. The sandbox is the product here, so exercising it
// against a fake would prove nothing.
func newTestRunner(t *testing.T) *Runner {
	t.Helper()

	cfg := config.Default()
	cfg.PayTo = "0.0.1234"
	cfg.NodeID = "node_test"
	// The dataset tests serve from loopback, which the default policy blocks
	// precisely so a renter cannot reach a provider's LAN. Opting in here is
	// the same switch an operator running a local mirror would use.
	cfg.Dataset.AllowPrivate = true
	cfg.Dataset.AllowHTTP = true
	cfg.Limits.MaxSeconds = 60

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	run, err := New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skipf("no usable Docker daemon: %v", err)
	}
	return run
}

// testJobID keeps container and volume names unique across runs, so a job left
// behind by an earlier failure cannot collide with a fresh one.
func testJobID(t *testing.T) string {
	t.Helper()
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("random id: %v", err)
	}
	return hex.EncodeToString(raw[:])
}

func TestValidateSpecRejectsImagesOffTheAllowlist(t *testing.T) {
	run := newTestRunner(t)

	err := run.ValidateSpec(Spec{Image: "evil/miner:latest"})
	if err == nil {
		t.Fatal("expected an image off the allowlist to be rejected")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("error should mention the allowlist, got: %v", err)
	}
}

func TestValidateSpecRejectsScriptPathTraversal(t *testing.T) {
	run := newTestRunner(t)

	err := run.ValidateSpec(Spec{
		Image:  testImage,
		Script: &Script{Filename: "../../etc/passwd", ContentBase64: ""},
	})
	if err == nil {
		t.Fatal("expected a script filename containing a path to be rejected")
	}
}

func TestRunJobToCompletion(t *testing.T) {
	run := newTestRunner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	script := "import pathlib\n" +
		"print('hello from cleargate')\n" +
		"pathlib.Path('/out/result.txt').write_text('42\\n')\n"

	job, err := run.Start(ctx, testJobID(t), "test-token", Spec{
		Image:   testImage,
		Cmd:     []string{"python", "job.py"},
		Script:  &Script{Filename: "job.py", ContentBase64: base64.StdEncoding.EncodeToString([]byte(script))},
		Timeout: 60,
	})
	if err != nil {
		t.Fatalf("start job: %v", err)
	}

	select {
	case <-job.Done():
	case <-ctx.Done():
		t.Fatal("job did not finish before the test deadline")
	}

	if job.Status() != StatusSucceeded {
		t.Fatalf("expected the job to succeed, got %s (%+v)", job.Status(), job.State())
	}

	var output strings.Builder
	for _, line := range job.Logs() {
		output.WriteString(line.Text)
		output.WriteString("\n")
	}
	if !strings.Contains(output.String(), "hello from cleargate") {
		t.Fatalf("expected the script's output in the log, got: %q", output.String())
	}

	// The artifact must contain what the job wrote to the one writable mount.
	var artifact strings.Builder
	if err := run.Artifact(ctx, job, &artifact); err != nil {
		t.Fatalf("download artifact: %v", err)
	}
	if !strings.Contains(artifact.String(), "result.txt") {
		t.Fatal("expected result.txt in the artifact tarball")
	}
}

func TestJobHasNoNetwork(t *testing.T) {
	run := newTestRunner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// If the sandbox leaks network access, this resolves and the test fails.
	script := "import socket\n" +
		"try:\n" +
		"    socket.create_connection(('1.1.1.1', 53), timeout=3)\n" +
		"    print('NETWORK REACHABLE')\n" +
		"except Exception as error:\n" +
		"    print('network blocked:', type(error).__name__)\n"

	job, err := run.Start(ctx, testJobID(t), "test-token", Spec{
		Image:   testImage,
		Cmd:     []string{"python", "net.py"},
		Script:  &Script{Filename: "net.py", ContentBase64: base64.StdEncoding.EncodeToString([]byte(script))},
		Timeout: 60,
	})
	if err != nil {
		t.Fatalf("start job: %v", err)
	}

	select {
	case <-job.Done():
	case <-ctx.Done():
		t.Fatal("job did not finish before the test deadline")
	}

	for _, line := range job.Logs() {
		if strings.Contains(line.Text, "NETWORK REACHABLE") {
			t.Fatal("the job sandbox has network access; it must run with NetworkMode=none")
		}
	}
}

func TestJobTimeoutIsEnforced(t *testing.T) {
	run := newTestRunner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	job, err := run.Start(ctx, testJobID(t), "test-token", Spec{
		Image:   testImage,
		Cmd:     []string{"python", "-c", "import time; time.sleep(600)"},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("start job: %v", err)
	}

	select {
	case <-job.Done():
	case <-ctx.Done():
		t.Fatal("the wall-clock limit did not stop the job")
	}

	if job.Status() != StatusTimeout {
		t.Fatalf("expected status timeout, got %s", job.Status())
	}
}

func TestJobTokenAuthorization(t *testing.T) {
	job := newJob("abc", "correct-token", Spec{Image: "python:3.11-slim"}, false)

	if !job.AuthorizedBy("correct-token") {
		t.Fatal("the issued token should authorize its own job")
	}
	if job.AuthorizedBy("wrong-token") {
		t.Fatal("a different token must not authorize the job")
	}
	if job.AuthorizedBy("") {
		t.Fatal("an empty token must never authorize anything")
	}
}

// A job killed while it is still staging must actually stop. Settlement can
// fail after staging has begun, and when it does the node must not finish
// downloading a dataset — and then run it — for a payment that never landed.
func TestKillDuringStagingStopsTheJob(t *testing.T) {
	run := newTestRunner(t)

	// A dataset that trickles: slow enough to hold the job in staging, while the
	// handler watches the request context so it returns the moment the download
	// is cancelled. A handler that blocked outright would wedge httptest.Close.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "268435456") // 256 MiB, never delivered
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Chunks large enough to fill the socket buffer, so once the download is
		// cancelled the next Write fails immediately rather than disappearing
		// into the kernel and leaving this handler running for the full loop.
		chunk := make([]byte, 1<<20)
		for range 256 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()
	// Registered second so it runs first: a cancelled download can leave the
	// connection looking active to httptest, which would stall Close.
	defer server.CloseClientConnections()

	id := testJobID(t)
	job, err := run.Start(context.Background(), id, "token", Spec{
		Image:   "python:3.11-slim",
		Cmd:     []string{"python", "-c", "print('should never run')"},
		Dataset: &Dataset{URL: server.URL + "/data.bin"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait until the download is genuinely in flight. Status alone is not
	// enough: Start marks the job "staging" synchronously, so killing on that
	// signal would test nothing but the fast path.
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(job.State().Stage, "downloading dataset ") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stage := job.State().Stage
	if !strings.Contains(stage, "downloading dataset ") {
		t.Fatalf("download never started; stage is %q, status %s", stage, job.Status())
	}
	t.Logf("killing mid-download, stage was %q", stage)

	if err := run.Kill(context.Background(), job); err != nil {
		t.Fatalf("kill: %v", err)
	}

	select {
	case <-job.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("killing a staging job did not stop it")
	}

	if status := job.Status(); status != StatusKilled {
		t.Fatalf("status = %s, want killed", status)
	}

	// The container must never have started, and nothing may be left behind.
	containers, err := run.docker.ListContainersByLabel(context.Background(), jobLabel+"="+id)
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	for _, container := range containers {
		if strings.Contains(strings.ToLower(container.State), "running") {
			t.Fatalf("container %s is still running after the kill", container.ID)
		}
	}
}

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handleJobState reports progress. Job-token gated.
func (s *Server) handleJobState(w http.ResponseWriter, r *http.Request) {
	job, ok := s.authorizeJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, job.State())
}

// handleJobLogs streams output as Server-Sent Events, or returns the retained
// output as JSON when the caller asks for JSON. Job-token gated.
func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	job, ok := s.authorizeJob(w, r)
	if !ok {
		return
	}

	if r.URL.Query().Get("follow") != "1" {
		writeJSON(w, http.StatusOK, map[string]any{"lines": job.Logs()})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	backlog, lines := job.Subscribe()
	defer job.Unsubscribe(lines)

	for _, line := range backlog {
		if !writeSSE(w, "log", line) {
			return
		}
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return

		case line, open := <-lines:
			if !open {
				// The job's output has ended; tell the client how it finished
				// so a CLI can stop following without polling.
				writeSSE(w, "end", job.State())
				flusher.Flush()
				return
			}
			if !writeSSE(w, "log", line) {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
	return err == nil
}

// handleJobArtifact streams the job's output directory as a tar. Job-token gated.
func (s *Server) handleJobArtifact(w http.ResponseWriter, r *http.Request) {
	job, ok := s.authorizeJob(w, r)
	if !ok {
		return
	}

	if !job.Status().IsTerminal() {
		writeError(w, http.StatusConflict, "job is still running; artifacts are available once it finishes")
		return
	}

	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", job.ID+".tar"))

	if err := s.runner.Artifact(r.Context(), job, w); err != nil {
		// Headers are already sent, so this cannot become a clean error status.
		// Log it and cut the stream; the client sees a truncated tar.
		s.log.Error("artifact stream failed", "job", job.ID, "error", err)
	}
}

// handleJobStop kills a running job early. Job-token gated.
//
// Flat-fee jobs are paid up front, so stopping does not refund anything —
// forward-payment is what makes refunds unnecessary by design. Metered leases
// (M3) are where stopping actually saves the renter money.
func (s *Server) handleJobStop(w http.ResponseWriter, r *http.Request) {
	job, ok := s.authorizeJob(w, r)
	if !ok {
		return
	}

	if err := s.runner.Kill(r.Context(), job); err != nil {
		writeError(w, http.StatusInternalServerError, "could not stop the job: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job.State())
}

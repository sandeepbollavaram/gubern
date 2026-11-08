package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/eventlog"
)

// Runs and their event streams (milestone D, docs/design/event-log.md).

// SetEventLog wires the persisted run event log. With it, structured event
// streams (submit with ?events=1) are recorded, resumable with
// Last-Event-ID, and readable at /api/runs/*.
func (s *Server) SetEventLog(l *eventlog.Log) { s.events = l }

// sseHeartbeatInterval is how often an idle event stream gets a comment
// line, so proxies and clients see the connection is alive.
// GUBERN_SSE_HEARTBEAT overrides it (1s to 5m).
func sseHeartbeatInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("GUBERN_SSE_HEARTBEAT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Second && d <= 5*time.Minute {
			return d
		}
	}
	return 15 * time.Second
}

// lastEventID is the sequence number a resuming client already has: the
// Last-Event-ID header (sent by EventSource on reconnect) or ?after=.
func lastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// submitLoggedRun starts an agent run recorded in the event log and streams
// it. The run is detached from the request: a client that disconnects can
// resume with Last-Event-ID; with nobody watching it is cancelled after the
// log's grace. An Idempotency-Key that the caller already used attaches to
// that run instead of starting another.
func (s *Server) submitLoggedRun(w http.ResponseWriter, r *http.Request, message, sessionID string) {
	s.startLoggedRun(w, r, message, sessionID, "")
}

// startLoggedRun is submitLoggedRun, optionally continuing the checkpoint of
// the interrupted run resumeFrom (already claimed by the caller).
func (s *Server) startLoggedRun(w http.ResponseWriter, r *http.Request, message, sessionID, resumeFrom string) {
	idem := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idem) > 128 {
		http.Error(w, "Idempotency-Key too long", http.StatusBadRequest)
		return
	}
	run, runCtx, existing, err := s.events.StartRun(r.Context(), sessionID, principalID(r), idem)
	if err != nil {
		http.Error(w, "starting run failed", http.StatusInternalServerError)
		return
	}
	if existing {
		if run.SessionID != sessionID {
			http.Error(w, "Idempotency-Key already used for another session", http.StatusConflict)
			return
		}
	} else {
		ctx := agentevents.WithSink(runCtx, func(ev agentevents.Event) {
			_, _ = s.events.Append(run.ID, ev)
		})
		ctx = agentevents.WithRunID(ctx, run.ID)
		if resumeFrom != "" {
			if err := s.events.ClaimResume(resumeFrom, run.ID); err != nil {
				s.events.Finish(run.ID, eventlog.StatusFailed)
				s.writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			ctx = agentevents.WithResume(ctx, resumeFrom)
		}
		ch, serr := s.agentRuntime.Submit(ctx, message, sessionID)
		if serr != nil {
			s.events.Finish(run.ID, eventlog.StatusFailed)
			if errors.Is(serr, ErrAgentBusy) {
				http.Error(w, "agent runtime busy", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, serr.Error(), http.StatusInternalServerError)
			return
		}
		go func() {
			for range ch { //nolint:revive // the text arrives as events; drain to completion
			}
			status := eventlog.StatusDone
			if runCtx.Err() != nil {
				status = eventlog.StatusCancelled
			}
			s.events.Finish(run.ID, status)
		}()
	}
	w.Header().Set("X-Gubern-Run-Id", run.ID)
	s.streamRun(w, r, run.ID, lastEventID(r))
}

// writeSSE writes one event with its sequence number as the SSE id.
func writeSSE(w http.ResponseWriter, ev agentevents.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, data)
}

// streamRun streams run's events after seq after: the stored ones, then live
// ones, with heartbeats, ending with a "done" event once the run finished.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request, runID string, after int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	allowLongResponse(w)
	sub, err := s.events.Subscribe(runID, after)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	last := after
	emit := func(ev agentevents.Event) {
		if ev.Seq <= last { // never send an event the client already has
			return
		}
		writeSSE(w, ev)
		last = ev.Seq
	}
	finish := func() {
		run, _ := s.events.Get(runID)
		data, _ := json.Marshal(map[string]any{"run_id": runID, "status": run.Status, "last_seq": last})
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", data)
		flusher.Flush()
	}
	for _, ev := range sub.Replay {
		emit(ev)
	}
	flusher.Flush()
	beat := time.NewTicker(s.sseHeartbeat)
	defer beat.Stop()
	for {
		if sub.Live == nil {
			finish()
			return
		}
		select {
		case <-r.Context().Done():
			sub.Close()
			return
		case <-beat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-sub.Live:
			if open {
				emit(ev)
				flusher.Flush()
				continue
			}
			// The run finished, or this stream fell behind and was cut off:
			// resubscribe from the last event sent (the store has the rest).
			next, err := s.events.Subscribe(runID, last)
			if err != nil {
				finish()
				return
			}
			sub = next
			for _, ev := range sub.Replay {
				emit(ev)
			}
			flusher.Flush()
		}
	}
}

// runAccess loads run id and checks the caller may use scope on its session.
func (s *Server) loadRun(w http.ResponseWriter, r *http.Request, scope authz.Scope) (eventlog.Run, bool) {
	if s.events == nil {
		http.Error(w, "event log not configured", http.StatusServiceUnavailable)
		return eventlog.Run{}, false
	}
	run, err := s.events.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return eventlog.Run{}, false
	}
	if !s.requireAccess(w, r, scope, resSession, run.SessionID) {
		return eventlog.Run{}, false
	}
	return run, true
}

// handleRunsList serves GET /api/runs?session_id=&limit=.
func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	out := []eventlog.Run{}
	if s.events == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"runs": out})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.events.Runs(r.URL.Query().Get("session_id"), limit)
	if err != nil {
		http.Error(w, "listing runs failed", http.StatusInternalServerError)
		return
	}
	for _, run := range runs {
		if s.canAccess(r, authz.SessionsRead, resSession, run.SessionID) {
			out = append(out, run)
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

// handleRunGet serves GET /api/runs/{id}.
func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	if run, ok := s.loadRun(w, r, authz.SessionsRead); ok {
		s.writeJSON(w, http.StatusOK, run)
	}
}

// handleRunEvents serves GET /api/runs/{id}/events: an SSE stream resuming
// after Last-Event-ID (or ?after=), or the stored events as JSON.
func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r, authz.SessionsRead)
	if !ok {
		return
	}
	if wantsSSE(r) {
		s.streamRun(w, r, run.ID, lastEventID(r))
		return
	}
	sub, err := s.events.Subscribe(run.ID, lastEventID(r))
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	sub.Close()
	events := sub.Replay
	if events == nil {
		events = []agentevents.Event{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"run": run, "events": events})
}

// handleRunCancel serves POST /api/runs/{id}/cancel.
func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r, authz.AgentsRun)
	if !ok {
		return
	}
	if !s.events.Cancel(run.ID) {
		s.writeJSON(w, http.StatusConflict, map[string]any{"error": "run is not running", "status": run.Status})
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "cancelling": true})
}

// resumableCheckpoint is the part of an agent checkpoint the API needs to
// restart the run's task (the agents package owns the full format).
type resumableCheckpoint struct {
	Session  string `json:"session"`
	Task     string `json:"task"`
	Check    string `json:"check"`
	ReadOnly bool   `json:"read_only"`
}

func (c resumableCheckpoint) message() string {
	m := "/agent "
	if c.ReadOnly {
		m += "--read-only "
	}
	if c.Check != "" {
		m += `--check "` + c.Check + `" `
	}
	return m + c.Task
}

// handleRunResume serves POST /api/runs/{id}/resume: an interrupted /agent
// run continues from its last checkpoint as a new run in the same session,
// streamed like a submit. Recorded steps are not re-run, and a side effect
// that was in flight is never repeated (see agents.LoopConfig.Resume). Each
// interrupted run can be resumed once.
func (s *Server) handleRunResume(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r, authz.AgentsRun)
	if !ok {
		return
	}
	if run.Status != eventlog.StatusInterrupted {
		s.writeJSON(w, http.StatusConflict, map[string]any{"error": "only an interrupted run can be resumed", "status": run.Status})
		return
	}
	data, resumedBy, err := s.events.Checkpoint(run.ID)
	if errors.Is(err, eventlog.ErrNoCheckpoint) {
		s.writeJSON(w, http.StatusConflict, map[string]any{"error": "this run saved no checkpoint, so it can't be resumed; start it again"})
		return
	}
	if err != nil {
		http.Error(w, "reading the checkpoint failed", http.StatusInternalServerError)
		return
	}
	if resumedBy != "" {
		s.writeJSON(w, http.StatusConflict, map[string]any{"error": "already resumed", "resumed_by": resumedBy})
		return
	}
	var cp resumableCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil || cp.Task == "" || cp.Session != run.SessionID {
		http.Error(w, "the checkpoint is unreadable", http.StatusInternalServerError)
		return
	}
	if s.agentRuntime == nil {
		http.Error(w, "agent runtime not available", http.StatusServiceUnavailable)
		return
	}
	s.audit(principalID(r), "run.resumed", run.ID, map[string]any{"session": run.SessionID})
	s.startLoggedRun(w, r, cp.message(), run.SessionID, run.ID)
}

// handleActivity serves GET /api/activity?session_id=&limit=: the timeline of
// tool steps, approvals and run starts and ends the caller may read.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	out := []agentevents.Event{}
	if s.events == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"events": out})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := s.events.Activity(r.URL.Query().Get("session_id"), limit)
	if err != nil {
		http.Error(w, "reading activity failed", http.StatusInternalServerError)
		return
	}
	allowed := map[string]bool{}
	for _, ev := range evs {
		ok, seen := allowed[ev.SessionID]
		if !seen {
			ok = s.canAccess(r, authz.SessionsRead, resSession, ev.SessionID)
			allowed[ev.SessionID] = ok
		}
		if ok {
			out = append(out, ev)
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

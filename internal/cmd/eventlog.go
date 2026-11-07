package cmd

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/eventlog"
)

// openEventLog opens the persisted run event log beside the key store
// (milestone D). GUBERN_EVENT_RETENTION (default 168h) bounds how long ended
// runs are kept; GUBERN_RUN_DETACH_GRACE (default 2m) is how long a run with
// nobody watching keeps going before it is cancelled.
func openEventLog(keyStorePath string, log *slog.Logger) (*eventlog.Log, error) {
	return eventlog.Open(filepath.Join(filepath.Dir(keyStorePath), "events.db"), eventlog.Options{
		Retention:   envDuration(log, "GUBERN_EVENT_RETENTION", time.Hour, 365*24*time.Hour),
		DetachGrace: envDuration(log, "GUBERN_RUN_DETACH_GRACE", time.Second, 24*time.Hour),
	})
}

// envDuration reads a duration env var within [lo, hi]; 0 (the default)
// when unset or invalid.
func envDuration(log *slog.Logger, name string, lo, hi time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < lo || d > hi {
		log.Warn("ignoring invalid duration", "env", name, "value", v)
		return 0
	}
	return d
}

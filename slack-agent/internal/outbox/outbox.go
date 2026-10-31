// Package outbox reads the poller's events. It never writes there: the
// directory belongs to another user and is only group-readable.
package outbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gengwg/oncall-copilot/slack-agent/internal/event"
)

// Scan returns every complete event in dir, oldest first. Files still being
// written end in .tmp and are skipped; an unreadable file is reported and skipped.
func Scan(dir string) ([]event.Event, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("outbox: %w", err)
	}
	var out []event.Event
	var bad []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		n := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(n)
		if err != nil {
			bad = append(bad, filepath.Base(n))
			continue
		}
		var ev event.Event
		if err := json.Unmarshal(b, &ev); err != nil || ev.IRMID == "" {
			bad = append(bad, filepath.Base(n))
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS < out[j].TS
		}
		return out[i].IRMID < out[j].IRMID
	})
	if len(bad) > 0 {
		return out, fmt.Errorf("outbox: %d unreadable event(s): %v", len(bad), bad)
	}
	return out, nil
}

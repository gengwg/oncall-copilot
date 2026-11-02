package event

import (
	"encoding/json"
	"os"
	"testing"
)

// The fixtures are what the poller and store wrote before the pager boundary.
// They must keep decoding unchanged so briefs already in flight survive.
func TestLegacyFixturesDecode(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-event.json")
	if err != nil {
		t.Fatal(err)
	}
	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("legacy event: %v", err)
	}
	if ev.ID != "S1" || ev.Ref != "501" {
		t.Fatalf("legacy event: %+v", ev)
	}

	raw, err = os.ReadFile("testdata/legacy-record.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("legacy record: %v", err)
	}
	if rec.ID != "S1" || rec.Event.ID != "S1" || rec.Event.Ref != "501" {
		t.Fatalf("legacy record: %+v", rec)
	}
	if rec.SlackTS == "" || rec.PostedAt == "" || !rec.AckDecided || rec.AckedAt == "" {
		t.Fatalf("legacy record lost its delivery or ack state: %+v", rec)
	}
}

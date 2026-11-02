package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func str(s string) *string { return &s }

func sameStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestEventLegacyDecode(t *testing.T) {
	var ev Event
	readFixture(t, "legacy-event.json", &ev)
	want := Event{
		Pager: "irm", ID: "S1", Ref: "501", URL: "https://slack.example/archives/C1/p501",
		Alertname: "SnapX", Severity: "critical", Cluster: "c30", Node: "gpu-9",
		StartsAt: "2026-10-25T09:04:00Z", Fault: "unknown", Verdict: "unknown",
		FailureMode: "unknown", Brief: "BRIEF SnapX on c30", TS: "2026-10-25T09:05:00Z",
	}
	assertEvent(t, ev, want)
}

func assertEvent(t *testing.T, got, want Event) {
	t.Helper()
	if !sameStrPtr(got.Degraded, want.Degraded) || !sameStrPtr(got.DCDraft, want.DCDraft) {
		t.Fatalf("pointer fields: got %+v want %+v", got, want)
	}
	got.Degraded, got.DCDraft, want.Degraded, want.DCDraft = nil, nil, nil, nil
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestEventNewDecode(t *testing.T) {
	raw := `{"pager":"fake","id":"X9","ref":"PD-7","url":"https://p/x9","alertname":"A","severity":"warning",
	 "cluster":"c","node":"n","starts_at":"2026-01-01T00:00:00Z","fault":"f","verdict":"v","failure_mode":"m",
	 "degraded":"yes","dc_draft":"/d/x9.md","brief":"B","ts":"T"}`
	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	assertEvent(t, ev, Event{Pager: "fake", ID: "X9", Ref: "PD-7", URL: "https://p/x9", Alertname: "A",
		Severity: "warning", Cluster: "c", Node: "n", StartsAt: "2026-01-01T00:00:00Z", Fault: "f",
		Verdict: "v", FailureMode: "m", Degraded: str("yes"), DCDraft: str("/d/x9.md"), Brief: "B", TS: "T"})
}

func TestEventDecodeBothKeySets(t *testing.T) {
	// The Task 2 poller writes both; the new keys win and the legacy ones add nothing.
	raw := `{"pager":"irm","id":"S5","ref":"505","url":"u","irm_id":"S5","irm_number":505,"irm_url":"u"}`
	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Pager != "irm" || ev.ID != "S5" || ev.Ref != "505" || ev.URL != "u" {
		t.Fatalf("%+v", ev)
	}
}

func TestEventDecodeNullIRMNumberIsEmptyRef(t *testing.T) {
	for _, raw := range []string{
		`{"pager":"irm","id":"S6","ref":"","irm_id":"S6","irm_number":null}`,
		`{"irm_id":"S6","irm_number":null}`,
		`{"irm_id":"S6"}`,
	} {
		var ev Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.ID != "S6" || ev.Ref != "" {
			t.Fatalf("%s -> %+v; a missing number must be \"\", never \"0\"", raw, ev)
		}
	}
}

func TestEventMarshalDualWritesForIRM(t *testing.T) {
	b, err := json.Marshal(Event{Pager: "irm", ID: "S1", Ref: "501", URL: "https://x/501"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["pager"] != "irm" || m["id"] != "S1" || m["ref"] != "501" || m["url"] != "https://x/501" {
		t.Fatalf("new keys: %s", b)
	}
	if m["irm_id"] != "S1" || m["irm_number"] != float64(501) || m["irm_url"] != "https://x/501" {
		t.Fatalf("legacy keys: %s", b)
	}
	// The previous agent must still be able to read what we write.
	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.ID != "S1" || back.Ref != "501" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}

func TestEventMarshalOmitsNonNumericLegacyNumber(t *testing.T) {
	b, _ := json.Marshal(Event{Pager: "irm", ID: "S1", Ref: ""})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["irm_number"]; ok {
		t.Fatalf("irm_number must be omitted when Ref is not numeric: %s", b)
	}
	if m["irm_id"] != "S1" {
		t.Fatalf("irm_id missing: %s", b)
	}
}

func TestEventMarshalNoLegacyForOthers(t *testing.T) {
	b, err := json.Marshal(Event{Pager: "fake", ID: "F1", Ref: "9", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"irm_id", "irm_number", "irm_url"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s must not be written for pager fake: %s", k, b)
		}
	}
	if m["pager"] != "fake" || m["id"] != "F1" {
		t.Fatalf("%s", b)
	}
}

func TestRecordLegacyDecode(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fixture, id string
		want        Record
	}{
		{"legacy-record.json", "S1", Record{
			ID: "S1", SlackTS: "1761383100.000100", PostedAt: "2026-10-25T09:05:10Z", PostAttempts: 1,
			AckDecided: true, AckedAt: "2026-10-25T09:05:11Z",
			Event: Event{Pager: "irm", ID: "S1", Ref: "501", URL: "https://slack.example/archives/C1/p501",
				Alertname: "SnapX", Severity: "critical", Cluster: "c30", Node: "gpu-9",
				StartsAt: "2026-10-25T09:04:00Z", Fault: "unknown", Verdict: "unknown",
				FailureMode: "unknown", Brief: "BRIEF SnapX on c30", TS: "2026-10-25T09:05:00Z"},
		}},
		{"legacy-record-deferred.json", "S2", Record{
			ID: "S2", Deferred: true, DeferredAt: "2026-10-25T03:05:12Z", AckDecided: true,
			AckNote: "off-shift: held for the morning digest", UpdatedAt: "2026-10-25T03:05:12Z",
			Event: Event{Pager: "irm", ID: "S2", Ref: "502", URL: "https://slack.example/archives/C1/p502",
				Alertname: "SnapY", Severity: "warning", Cluster: "c31", Node: "gpu-3",
				StartsAt: "2026-10-25T03:04:00Z", Fault: "xid79", Verdict: "real",
				FailureMode: "gpu_fallen_off_bus", DCDraft: str("/tmp/dc/draft-S2.md"),
				Brief: "BRIEF SnapY on c31", TS: "2026-10-25T03:05:00Z"},
		}},
	} {
		raw, err := os.ReadFile("testdata/" + c.fixture)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, c.id+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(c.id)
		if err != nil || got == nil {
			t.Fatalf("%s: %v %v", c.fixture, got, err)
		}
		assertEvent(t, got.Event, c.want.Event)
		g, w := *got, c.want
		g.Event, w.Event = Event{}, Event{}
		if g != w {
			t.Fatalf("%s:\ngot  %+v\nwant %+v", c.fixture, g, w)
		}
	}
}

func TestRecordWritesBothIDs(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r := &Record{ID: "S3", Event: Event{Pager: "irm", ID: "S3", Ref: "503", URL: "u"}, SlackTS: "1.2"}
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.dir, "S3.json"))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "S3" || m["irm_id"] != "S3" {
		t.Fatalf("record keys: %s", raw)
	}
	ev, _ := m["event"].(map[string]any)
	if ev["id"] != "S3" || ev["irm_id"] != "S3" || ev["irm_number"] != float64(503) || ev["irm_url"] != "u" {
		t.Fatalf("embedded event must carry the legacy keys too: %s", raw)
	}
	got, err := s.Get("S3")
	if err != nil || got.ID != "S3" || got.Event.Ref != "503" || got.SlackTS != "1.2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSaveRejectsEmptyID(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.Save(&Record{}); err == nil || err.Error() != "record without id" {
		t.Fatalf("got %v", err)
	}
}

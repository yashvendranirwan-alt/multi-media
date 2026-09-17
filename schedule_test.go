package schedule

import (
	"testing"

	"media-sequencer/internal/models"
)

// testWindow has a 10s loop (5s + 3s + 2s) inside a 24s cycle, so the cycle is
// deliberately not a whole number of loops: 2.4 passes.
func testWindow() models.Window {
	return models.Window{
		ID:   "w1",
		Name: "Lobby",
		Items: []models.PlaylistItem{
			{ID: "i1", MediaID: "m1", DurationMs: 5000},
			{ID: "i2", MediaID: "m2", DurationMs: 3000},
			{ID: "i3", MediaID: "m3", DurationMs: 2000},
		},
		AnchorMs: 1000,
		CycleMs:  24000,
	}
}

func assertFrame(t *testing.T, got Frame, wantMedia string, wantStart, wantEnd int64) {
	t.Helper()
	if got.MediaID != wantMedia {
		t.Errorf("mediaID = %q, want %q", got.MediaID, wantMedia)
	}
	if got.StartAtMs != wantStart {
		t.Errorf("startAtMs = %d, want %d", got.StartAtMs, wantStart)
	}
	if got.EndAtMs != wantEnd {
		t.Errorf("endAtMs = %d, want %d", got.EndAtMs, wantEnd)
	}
}

func TestResolveWithinFirstLoop(t *testing.T) {
	w := testWindow()
	cases := []struct {
		name       string
		now        int64
		media      string
		start, end int64
		index      int
	}{
		{"first instant of cycle", 1000, "m1", 1000, 6000, 0},
		{"last instant of item 0", 5999, "m1", 1000, 6000, 0},
		{"boundary belongs to item 1", 6000, "m2", 6000, 9000, 1},
		{"item 2", 9500, "m3", 9000, 11000, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(w, nil, tc.now)
			if got.Source != SourcePlaylist {
				t.Fatalf("source = %q, want playlist", got.Source)
			}
			assertFrame(t, got, tc.media, tc.start, tc.end)
			if got.ItemIndex != tc.index {
				t.Errorf("itemIndex = %d, want %d", got.ItemIndex, tc.index)
			}
		})
	}
}

func TestPlaylistRepeatsInsideCycle(t *testing.T) {
	w := testWindow()
	// 10s after the cycle started the playlist starts over from item 0 without
	// any blank filler in between.
	got := Resolve(w, nil, 11000)
	assertFrame(t, got, "m1", 11000, 16000)
	if got.LoopIndex != 1 {
		t.Errorf("loopIndex = %d, want 1", got.LoopIndex)
	}
	if got.Source != SourcePlaylist {
		t.Errorf("source = %q, want playlist", got.Source)
	}
}

func TestItemIsTruncatedAtCycleBoundary(t *testing.T) {
	w := testWindow()
	// Cycle ends at 1000+24000 = 25000, mid-way through item 0 of the third
	// pass, which runs 21000..26000.
	got := Resolve(w, nil, 24000)
	assertFrame(t, got, "m1", 21000, 25000)
	if got.CycleElapsedMs != 23000 {
		t.Errorf("cycleElapsedMs = %d, want 23000", got.CycleElapsedMs)
	}
}

func TestCycleRestartsFromFirstItem(t *testing.T) {
	w := testWindow()
	got := Resolve(w, nil, 25000)
	assertFrame(t, got, "m1", 25000, 30000)
	if got.CycleElapsedMs != 0 {
		t.Errorf("cycleElapsedMs = %d, want 0", got.CycleElapsedMs)
	}
	if got.LoopIndex != 0 {
		t.Errorf("loopIndex = %d, want 0", got.LoopIndex)
	}
}

func TestTimeBeforeAnchorStaysOnTimeline(t *testing.T) {
	w := testWindow()
	// A clock that is behind the anchor must not produce negative positions.
	got := Resolve(w, nil, 0)
	if got.Source != SourcePlaylist {
		t.Fatalf("source = %q, want playlist", got.Source)
	}
	if got.CycleElapsedMs != 23000 {
		t.Errorf("cycleElapsedMs = %d, want 23000", got.CycleElapsedMs)
	}
	if got.StartAtMs > 0 || got.EndAtMs <= 0 {
		t.Errorf("frame %d..%d does not contain t=0", got.StartAtMs, got.EndAtMs)
	}
}

func TestEmptyPlaylistIsIdle(t *testing.T) {
	w := testWindow()
	w.Items = nil
	got := Resolve(w, nil, 5000)
	if got.Source != SourceIdle {
		t.Fatalf("source = %q, want idle", got.Source)
	}
	if got.MediaID != "" {
		t.Errorf("mediaID = %q, want empty", got.MediaID)
	}
}

func TestZeroDurationItemsAreSkipped(t *testing.T) {
	w := testWindow()
	w.Items = append([]models.PlaylistItem{{ID: "bad", MediaID: "broken", DurationMs: 0}}, w.Items...)
	got := Resolve(w, nil, 1000)
	if got.MediaID != "m1" {
		t.Errorf("mediaID = %q, want m1", got.MediaID)
	}
}

func TestSyncOverridesEveryWindowOnTheSameInterval(t *testing.T) {
	a := testWindow()
	b := testWindow()
	b.ID = "w2"
	b.AnchorMs = 4321 // different phase
	b.Items = []models.PlaylistItem{{ID: "j1", MediaID: "m5", DurationMs: 7000}}

	sync := &models.SyncEvent{ID: "s1", MediaID: "m2", StartAtMs: 12000, DurationMs: 4000}

	for _, now := range []int64{12000, 13500, 15999} {
		fa := Resolve(a, sync, now)
		fb := Resolve(b, sync, now)
		if fa.Source != SourceSync || fb.Source != SourceSync {
			t.Fatalf("at %d sources = %q/%q, want sync", now, fa.Source, fb.Source)
		}
		if fa.MediaID != "m2" || fb.MediaID != "m2" {
			t.Errorf("at %d media = %q/%q, want m2 in both", now, fa.MediaID, fb.MediaID)
		}
		if fa.StartAtMs != fb.StartAtMs || fa.EndAtMs != fb.EndAtMs {
			t.Errorf("at %d windows disagree on the sync interval", now)
		}
	}
}

func TestPlaylistResumesUnderneathSync(t *testing.T) {
	w := testWindow()
	sync := &models.SyncEvent{ID: "s1", MediaID: "m9", StartAtMs: 12000, DurationMs: 4000}

	// Before the sync: item 0 of the second pass, 11000..16000.
	assertFrame(t, Resolve(w, nil, 12000), "m1", 11000, 16000)

	// The instant the sync ends the window is back on its own item, mid-way
	// through, with the playlist untouched.
	got := Resolve(w, sync, 16000)
	if got.Source != SourcePlaylist {
		t.Fatalf("source = %q, want playlist", got.Source)
	}
	assertFrame(t, got, "m2", 16000, 19000)

	resumed := Resolve(w, sync, 15000)
	if resumed.Source != SourceSync {
		t.Fatalf("source = %q, want sync at 15000", resumed.Source)
	}
}

func TestExpiredSyncIsIgnored(t *testing.T) {
	w := testWindow()
	sync := &models.SyncEvent{ID: "s1", MediaID: "m9", StartAtMs: 2000, DurationMs: 1000}
	got := Resolve(w, sync, 9000)
	if got.Source != SourcePlaylist {
		t.Errorf("source = %q, want playlist", got.Source)
	}
}

func TestResolveNext(t *testing.T) {
	w := testWindow()
	next := ResolveNext(w, nil, 3000)
	assertFrame(t, next, "m2", 6000, 9000)

	// While a sync is on screen, "next" is what comes back after it ends.
	sync := &models.SyncEvent{ID: "s1", MediaID: "m9", StartAtMs: 2000, DurationMs: 3000}
	next = ResolveNext(w, sync, 3000)
	if next.Source != SourcePlaylist || next.MediaID != "m1" {
		t.Errorf("next after sync = %q/%q, want playlist/m1", next.Source, next.MediaID)
	}
}

func TestFullTimelineHasNoGaps(t *testing.T) {
	w := testWindow()
	// Walk a whole cycle frame by frame and check the timeline is continuous:
	// every frame starts exactly where the previous one ended.
	now := w.AnchorMs
	end := w.AnchorMs + w.CycleMs
	var frames int
	for now < end {
		f := Resolve(w, nil, now)
		if f.StartAtMs != now {
			t.Fatalf("gap or overlap at %d: frame starts at %d", now, f.StartAtMs)
		}
		if f.EndAtMs <= f.StartAtMs {
			t.Fatalf("non-advancing frame at %d", now)
		}
		now = f.EndAtMs
		frames++
		if frames > 100 {
			t.Fatal("timeline did not terminate")
		}
	}
	if now != end {
		t.Errorf("cycle ended at %d, want %d", now, end)
	}
	// 2 full passes of 3 items + 1 truncated item.
	if frames != 7 {
		t.Errorf("frames = %d, want 7", frames)
	}
}

func TestFiveHourCycleDefault(t *testing.T) {
	const fiveHours = int64(5 * 60 * 60 * 1000)
	w := testWindow()
	w.CycleMs = fiveHours
	// 5h = 1800 passes of a 10s loop exactly, so the last item of the last
	// pass must end precisely on the cycle boundary.
	got := Resolve(w, nil, w.AnchorMs+fiveHours-1)
	if got.MediaID != "m3" {
		t.Errorf("mediaID = %q, want m3", got.MediaID)
	}
	if got.EndAtMs != w.AnchorMs+fiveHours {
		t.Errorf("endAtMs = %d, want %d", got.EndAtMs, w.AnchorMs+fiveHours)
	}
	if got := Resolve(w, nil, w.AnchorMs+fiveHours); got.ItemIndex != 0 {
		t.Errorf("itemIndex after cycle = %d, want 0", got.ItemIndex)
	}
}

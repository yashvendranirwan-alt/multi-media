package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"media-sequencer/internal/models"
)

func newTestStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := NewFileStore(Config{
		Path:          path,
		CycleMs:       5 * 60 * 60 * 1000,
		DefaultSyncMs: 15000,
		Now:           time.Now,
	})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestSeedsOnFirstRun(t *testing.T) {
	s, _ := newTestStore(t)
	snap := s.Snapshot()

	if len(snap.Windows) != 4 {
		t.Errorf("windows = %d, want 4", len(snap.Windows))
	}
	if len(snap.Media) != 9 {
		t.Errorf("media = %d, want 9", len(snap.Media))
	}
	if snap.Sync != nil {
		t.Error("a fresh store should have no sync event")
	}

	var blank, video bool
	for _, m := range snap.Media {
		switch m.Type {
		case models.MediaBlank:
			blank = true
		case models.MediaVideo:
			video = true
		}
		if m.DurationMs <= 0 {
			t.Errorf("media %s has no duration", m.ID)
		}
	}
	if !blank || !video {
		t.Error("seed must include blank and video media")
	}

	for _, w := range snap.Windows {
		if len(w.Items) == 0 {
			t.Errorf("window %s has an empty playlist", w.ID)
		}
		if w.LoopMs() <= 0 {
			t.Errorf("window %s has a zero-length loop", w.ID)
		}
		if w.CycleMs != snap.DefaultCycleMs {
			t.Errorf("window %s cycle = %d, want %d", w.ID, w.CycleMs, snap.DefaultCycleMs)
		}
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	s, path := newTestStore(t)

	media, err := s.AddMedia("Promo", models.MediaImage, "https://example.com/p.png", 4000)
	if err != nil {
		t.Fatalf("AddMedia: %v", err)
	}
	if _, err := s.AddItem("wn_lobby", media.ID, 0); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if _, err := s.SetSync(MediaM2, 0, 9000); err != nil {
		t.Fatalf("SetSync: %v", err)
	}

	reopened, err := NewFileStore(Config{Path: path, CycleMs: 5 * 60 * 60 * 1000, DefaultSyncMs: 15000})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	snap := reopened.Snapshot()
	if len(snap.Media) != 10 {
		t.Errorf("media after restart = %d, want 10", len(snap.Media))
	}
	var lobby models.Window
	for _, w := range snap.Windows {
		if w.ID == "wn_lobby" {
			lobby = w
		}
	}
	if len(lobby.Items) != 4 {
		t.Fatalf("lobby items after restart = %d, want 4", len(lobby.Items))
	}
	if lobby.Items[3].MediaID != media.ID {
		t.Errorf("appended item media = %q, want %q", lobby.Items[3].MediaID, media.ID)
	}
	if lobby.Items[3].DurationMs != 4000 {
		t.Errorf("item duration = %d, want the media default 4000", lobby.Items[3].DurationMs)
	}
	if snap.Sync == nil || snap.Sync.MediaID != MediaM2 {
		t.Error("sync event did not survive the restart")
	}
}

func TestAddItemBumpsVersion(t *testing.T) {
	s, _ := newTestStore(t)
	before := findWindow(t, s, "wn_lobby")

	after, err := s.AddItem("wn_lobby", MediaM5, 3000)
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if after.Version != before.Version+1 {
		t.Errorf("version = %d, want %d", after.Version, before.Version+1)
	}
	if got := after.LoopMs(); got != before.LoopMs()+3000 {
		t.Errorf("loop = %d, want %d", got, before.LoopMs()+3000)
	}
}

func TestRemoveItem(t *testing.T) {
	s, _ := newTestStore(t)
	w, err := s.RemoveItem("wn_lobby", "it_lobby_2")
	if err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	for _, it := range w.Items {
		if it.ID == "it_lobby_2" {
			t.Fatal("item was not removed")
		}
	}
	if _, err := s.RemoveItem("wn_lobby", "it_lobby_2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove error = %v, want ErrNotFound", err)
	}
}

func TestSnapshotIsADeepCopy(t *testing.T) {
	s, _ := newTestStore(t)
	snap := s.Snapshot()
	snap.Windows[0].Items[0].MediaID = "tampered"
	snap.Windows[0].Name = "tampered"

	fresh := s.Snapshot()
	if fresh.Windows[0].Items[0].MediaID == "tampered" || fresh.Windows[0].Name == "tampered" {
		t.Error("mutating a snapshot changed stored state")
	}
}

func TestValidation(t *testing.T) {
	s, _ := newTestStore(t)

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"unknown window", func() error { _, err := s.AddItem("nope", MediaM1, 0); return err }, ErrNotFound},
		{"unknown media", func() error { _, err := s.AddItem("wn_lobby", "nope", 0); return err }, ErrNotFound},
		{"sync unknown media", func() error { _, err := s.SetSync("nope", 0, 5000); return err }, ErrNotFound},
		{"empty label", func() error { _, err := s.AddMedia("", models.MediaImage, "u", 1000); return err }, ErrInvalid},
		{"bad type", func() error { _, err := s.AddMedia("X", models.MediaType("gif"), "u", 1000); return err }, ErrInvalid},
		{"missing url", func() error { _, err := s.AddMedia("X", models.MediaImage, "", 1000); return err }, ErrInvalid},
		{"bad duration", func() error { _, err := s.AddMedia("X", models.MediaImage, "u", 0); return err }, ErrInvalid},
		{"empty window name", func() error { _, err := s.CreateWindow(""); return err }, ErrInvalid},
		{"negative item duration", func() error { _, err := s.AddItem("wn_lobby", MediaM1, -5); return err }, ErrInvalid},
		{"negative sync duration", func() error { _, err := s.SetSync(MediaM1, 0, -5); return err }, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestBlankMediaNeedsNoURL(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.AddMedia("Gap", models.MediaBlank, "", 2000); err != nil {
		t.Errorf("blank media rejected: %v", err)
	}
}

func TestSyncDefaultsAndClear(t *testing.T) {
	s, _ := newTestStore(t)

	ev, err := s.SetSync(MediaM2, 0, 0)
	if err != nil {
		t.Fatalf("SetSync: %v", err)
	}
	if ev.DurationMs != 15000 {
		t.Errorf("duration = %d, want the configured default 15000", ev.DurationMs)
	}
	if ev.StartAtMs <= 0 {
		t.Error("startAtMs should default to now")
	}
	if !ev.ActiveAt(ev.StartAtMs + 1) {
		t.Error("sync should be active just after it starts")
	}
	if ev.ActiveAt(ev.EndAtMs()) {
		t.Error("sync should be over at exactly endAtMs")
	}

	if err := s.ClearSync(); err != nil {
		t.Fatalf("ClearSync: %v", err)
	}
	if s.Snapshot().Sync != nil {
		t.Error("sync was not cleared")
	}
}

func TestRestartCycleMovesAnchor(t *testing.T) {
	s, _ := newTestStore(t)
	before := findWindow(t, s, "wn_lobby")
	time.Sleep(2 * time.Millisecond)

	after, err := s.RestartCycle("wn_lobby")
	if err != nil {
		t.Fatalf("RestartCycle: %v", err)
	}
	if after.AnchorMs <= before.AnchorMs {
		t.Errorf("anchor = %d, want later than %d", after.AnchorMs, before.AnchorMs)
	}
	if len(after.Items) != len(before.Items) {
		t.Error("restarting the cycle must not change the playlist")
	}
}

func TestCreateAndDeleteWindow(t *testing.T) {
	s, _ := newTestStore(t)

	w, err := s.CreateWindow("Stairwell")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	if len(w.Items) != 0 {
		t.Error("a new window starts empty")
	}
	if len(s.Snapshot().Windows) != 5 {
		t.Error("window was not stored")
	}

	if err := s.DeleteWindow(w.ID); err != nil {
		t.Fatalf("DeleteWindow: %v", err)
	}
	if len(s.Snapshot().Windows) != 4 {
		t.Error("window was not deleted")
	}
	if err := s.DeleteWindow(w.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestResetRestoresSeed(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.AddItem("wn_lobby", MediaM5, 1000); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if _, err := s.SetSync(MediaM1, 0, 5000); err != nil {
		t.Fatalf("SetSync: %v", err)
	}

	if err := s.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	snap := s.Snapshot()
	if snap.Sync != nil {
		t.Error("reset should clear the sync event")
	}
	if len(findWindowIn(t, snap, "wn_lobby").Items) != 3 {
		t.Error("reset should restore the seeded playlist")
	}
}

func TestCycleConfigChangeAppliesOnReload(t *testing.T) {
	s, path := newTestStore(t)
	_ = s

	reopened, err := NewFileStore(Config{Path: path, CycleMs: 120000, DefaultSyncMs: 15000})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	for _, w := range reopened.Snapshot().Windows {
		if w.CycleMs != 120000 {
			t.Errorf("window %s cycle = %d, want 120000", w.ID, w.CycleMs)
		}
	}
}

func TestConcurrentWritesAreSafe(t *testing.T) {
	s, _ := newTestStore(t)
	done := make(chan error, 20)

	for i := 0; i < 10; i++ {
		go func() {
			_, err := s.AddItem("wn_lobby", MediaM1, 1000)
			done <- err
		}()
		go func() {
			s.Snapshot()
			done <- nil
		}()
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent op failed: %v", err)
		}
	}
	if got := len(findWindow(t, s, "wn_lobby").Items); got != 13 {
		t.Errorf("items = %d, want 13", got)
	}
}

func findWindow(t *testing.T, s *FileStore, id string) models.Window {
	t.Helper()
	return findWindowIn(t, s.Snapshot(), id)
}

func findWindowIn(t *testing.T, snap models.Snapshot, id string) models.Window {
	t.Helper()
	for _, w := range snap.Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("window %s not found", id)
	return models.Window{}
}

func TestEmptyPlaylistSerialisesAsArray(t *testing.T) {
	s, _ := newTestStore(t)
	w, err := s.CreateWindow("Atrium screen")
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	if w.Items == nil {
		t.Error("a new window returned a nil Items slice")
	}
	blob, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(blob, []byte(`"items":[]`)) {
		t.Errorf("items should encode as [], got %s", blob)
	}
}

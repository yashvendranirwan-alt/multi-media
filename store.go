// Package store keeps the media library, the window playlists and the current
// sync event on disk so the system survives a restart.
//
// The concrete implementation here writes a single JSON document atomically.
// Everything goes through the Store interface, so swapping in Postgres or
// SQLite later only means writing one more type (see README, "Swapping the
// storage engine").
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"media-sequencer/internal/models"
)

// Sentinel errors the API layer maps onto status codes.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid request")
)

// Store is the persistence contract for the whole application.
type Store interface {
	// Snapshot returns a deep copy of the current state.
	Snapshot() models.Snapshot
	AddMedia(label string, mediaType models.MediaType, url string, durationMs int64) (models.Media, error)
	CreateWindow(name string) (models.Window, error)
	DeleteWindow(windowID string) error
	// AddItem appends a media item to a window's sequence. durationMs of 0
	// falls back to the media's default duration.
	AddItem(windowID, mediaID string, durationMs int64) (models.Window, error)
	RemoveItem(windowID, itemID string) (models.Window, error)
	// RestartCycle moves the window's 5-hour cycle anchor to now.
	RestartCycle(windowID string) (models.Window, error)
	SetSync(mediaID string, startAtMs, durationMs int64) (models.SyncEvent, error)
	ClearSync() error
	// Reset restores the seed data. Useful during review and demos.
	Reset() error
	Close() error
}

// Config tunes the file store.
type Config struct {
	// Path is the JSON file the state lives in.
	Path string
	// CycleMs is the total play size of a window, 5 hours by default.
	CycleMs int64
	// DefaultSyncMs is the sync takeover length used when a request omits one.
	DefaultSyncMs int64
	// MediaBaseURL prefixes seeded media that the backend serves itself.
	Now func() time.Time
}

// document is the on-disk shape. It is versioned so a future migration can
// detect old files instead of silently misreading them.
type document struct {
	Version int               `json:"version"`
	Windows []models.Window   `json:"windows"`
	Media   []models.Media    `json:"media"`
	Sync    *models.SyncEvent `json:"sync"`
	SavedAt int64             `json:"savedAtMs"`
}

const documentVersion = 1

// FileStore is a Store backed by one JSON file guarded by a mutex.
type FileStore struct {
	mu  sync.RWMutex
	doc document
	cfg Config
}

var _ Store = (*FileStore)(nil)

// NewFileStore loads state from cfg.Path, seeding the file if it is missing.
func NewFileStore(cfg Config) (*FileStore, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("store: empty path")
	}
	if cfg.CycleMs <= 0 {
		return nil, fmt.Errorf("store: cycle duration must be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}

	s := &FileStore{cfg: cfg}
	raw, err := os.ReadFile(cfg.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.doc = seedDocument(s.nowMs(), cfg.CycleMs)
		if err := s.persist(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("store: read %s: %w", cfg.Path, err)
	default:
		if err := json.Unmarshal(raw, &s.doc); err != nil {
			return nil, fmt.Errorf("store: parse %s: %w", cfg.Path, err)
		}
		if s.doc.Version != documentVersion {
			return nil, fmt.Errorf("store: unsupported data file version %d", s.doc.Version)
		}
		s.migrateCycle()
	}
	return s, nil
}

func (s *FileStore) nowMs() int64 { return s.cfg.Now().UnixMilli() }

// migrateCycle applies the configured cycle length to windows loaded from disk
// so changing CYCLE_DURATION takes effect without deleting the data file.
func (s *FileStore) migrateCycle() {
	changed := false
	for i := range s.doc.Windows {
		if s.doc.Windows[i].CycleMs != s.cfg.CycleMs {
			s.doc.Windows[i].CycleMs = s.cfg.CycleMs
			changed = true
		}
	}
	if changed {
		_ = s.persist()
	}
}

// persist writes the document atomically: a temporary file in the same
// directory, flushed, then renamed over the target. A crash mid-write leaves
// the previous good file in place.
func (s *FileStore) persist() error {
	s.doc.Version = documentVersion
	s.doc.SavedAt = s.nowMs()

	raw, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode: %w", err)
	}

	dir := filepath.Dir(s.cfg.Path)
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("store: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("store: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("store: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	if err := os.Rename(tmpName, s.cfg.Path); err != nil {
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}

// Snapshot returns a deep copy so callers can never mutate stored state.
func (s *FileStore) Snapshot() models.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	windows := make([]models.Window, len(s.doc.Windows))
	for i, w := range s.doc.Windows {
		w.Items = append([]models.PlaylistItem(nil), w.Items...)
		windows[i] = w
	}
	media := append([]models.Media(nil), s.doc.Media...)

	var sync *models.SyncEvent
	if s.doc.Sync != nil {
		cp := *s.doc.Sync
		sync = &cp
	}

	return models.Snapshot{
		Windows:        windows,
		Media:          media,
		Sync:           sync,
		ServerTimeMs:   s.nowMs(),
		DefaultCycleMs: s.cfg.CycleMs,
		DefaultSyncMs:  s.cfg.DefaultSyncMs,
	}
}

func (s *FileStore) AddMedia(label string, mediaType models.MediaType, url string, durationMs int64) (models.Media, error) {
	if label == "" {
		return models.Media{}, fmt.Errorf("%w: label is required", ErrInvalid)
	}
	if !mediaType.Valid() {
		return models.Media{}, fmt.Errorf("%w: type must be image, video or blank", ErrInvalid)
	}
	if mediaType != models.MediaBlank && url == "" {
		return models.Media{}, fmt.Errorf("%w: url is required for %s media", ErrInvalid, mediaType)
	}
	if durationMs <= 0 {
		return models.Media{}, fmt.Errorf("%w: durationMs must be positive", ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	media := models.Media{
		ID:          newID("md"),
		Label:       label,
		Type:        mediaType,
		URL:         url,
		DurationMs:  durationMs,
		CreatedAtMs: s.nowMs(),
	}
	s.doc.Media = append(s.doc.Media, media)
	if err := s.persist(); err != nil {
		s.doc.Media = s.doc.Media[:len(s.doc.Media)-1]
		return models.Media{}, err
	}
	return media, nil
}

func (s *FileStore) CreateWindow(name string) (models.Window, error) {
	if name == "" {
		return models.Window{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowMs()
	w := models.Window{
		ID:          newID("wn"),
		Name:        name,
		Items:       []models.PlaylistItem{},
		AnchorMs:    now,
		CycleMs:     s.cfg.CycleMs,
		Version:     1,
		UpdatedAtMs: now,
	}
	s.doc.Windows = append(s.doc.Windows, w)
	if err := s.persist(); err != nil {
		s.doc.Windows = s.doc.Windows[:len(s.doc.Windows)-1]
		return models.Window{}, err
	}
	return w, nil
}

func (s *FileStore) DeleteWindow(windowID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.windowIndex(windowID)
	if idx < 0 {
		return fmt.Errorf("%w: window %s", ErrNotFound, windowID)
	}
	removed := s.doc.Windows[idx]
	s.doc.Windows = append(s.doc.Windows[:idx:idx], s.doc.Windows[idx+1:]...)
	if err := s.persist(); err != nil {
		s.doc.Windows = append(s.doc.Windows, removed)
		return err
	}
	return nil
}

func (s *FileStore) AddItem(windowID, mediaID string, durationMs int64) (models.Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.windowIndex(windowID)
	if idx < 0 {
		return models.Window{}, fmt.Errorf("%w: window %s", ErrNotFound, windowID)
	}
	media, ok := s.findMedia(mediaID)
	if !ok {
		return models.Window{}, fmt.Errorf("%w: media %s", ErrNotFound, mediaID)
	}
	// An omitted durationMs arrives as 0 and means "use the media's own
	// duration". A negative value is a caller mistake, so it is reported
	// rather than quietly rewritten.
	if durationMs < 0 {
		return models.Window{}, fmt.Errorf("%w: durationMs must not be negative", ErrInvalid)
	}
	if durationMs == 0 {
		durationMs = media.DurationMs
	}
	if durationMs <= 0 {
		return models.Window{}, fmt.Errorf("%w: durationMs must be positive", ErrInvalid)
	}

	now := s.nowMs()
	item := models.PlaylistItem{
		ID:         newID("it"),
		MediaID:    mediaID,
		DurationMs: durationMs,
		AddedAtMs:  now,
	}

	w := &s.doc.Windows[idx]
	before := w.Items
	w.Items = append(append([]models.PlaylistItem(nil), w.Items...), item)
	w.Version++
	w.UpdatedAtMs = now

	if err := s.persist(); err != nil {
		w.Items = before
		w.Version--
		return models.Window{}, err
	}
	return s.copyWindow(*w), nil
}

func (s *FileStore) RemoveItem(windowID, itemID string) (models.Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.windowIndex(windowID)
	if idx < 0 {
		return models.Window{}, fmt.Errorf("%w: window %s", ErrNotFound, windowID)
	}

	w := &s.doc.Windows[idx]
	before := w.Items
	next := make([]models.PlaylistItem, 0, len(w.Items))
	found := false
	for _, it := range w.Items {
		if it.ID == itemID {
			found = true
			continue
		}
		next = append(next, it)
	}
	if !found {
		return models.Window{}, fmt.Errorf("%w: item %s", ErrNotFound, itemID)
	}

	w.Items = next
	w.Version++
	w.UpdatedAtMs = s.nowMs()

	if err := s.persist(); err != nil {
		w.Items = before
		w.Version--
		return models.Window{}, err
	}
	return s.copyWindow(*w), nil
}

func (s *FileStore) RestartCycle(windowID string) (models.Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.windowIndex(windowID)
	if idx < 0 {
		return models.Window{}, fmt.Errorf("%w: window %s", ErrNotFound, windowID)
	}

	w := &s.doc.Windows[idx]
	before := w.AnchorMs
	now := s.nowMs()
	w.AnchorMs = now
	w.UpdatedAtMs = now

	if err := s.persist(); err != nil {
		w.AnchorMs = before
		return models.Window{}, err
	}
	return s.copyWindow(*w), nil
}

func (s *FileStore) SetSync(mediaID string, startAtMs, durationMs int64) (models.SyncEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.findMedia(mediaID); !ok {
		return models.SyncEvent{}, fmt.Errorf("%w: media %s", ErrNotFound, mediaID)
	}
	// As with playlist items, 0 means "use the configured default" while a
	// negative value is rejected as a caller mistake.
	if durationMs < 0 {
		return models.SyncEvent{}, fmt.Errorf("%w: durationMs must not be negative", ErrInvalid)
	}
	if durationMs == 0 {
		durationMs = s.cfg.DefaultSyncMs
	}
	if durationMs <= 0 {
		return models.SyncEvent{}, fmt.Errorf("%w: durationMs must be positive", ErrInvalid)
	}

	now := s.nowMs()
	if startAtMs <= 0 {
		startAtMs = now
	}

	event := models.SyncEvent{
		ID:          newID("sy"),
		MediaID:     mediaID,
		StartAtMs:   startAtMs,
		DurationMs:  durationMs,
		CreatedAtMs: now,
	}
	before := s.doc.Sync
	s.doc.Sync = &event
	if err := s.persist(); err != nil {
		s.doc.Sync = before
		return models.SyncEvent{}, err
	}
	return event, nil
}

func (s *FileStore) ClearSync() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	before := s.doc.Sync
	s.doc.Sync = nil
	if err := s.persist(); err != nil {
		s.doc.Sync = before
		return err
	}
	return nil
}

func (s *FileStore) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	before := s.doc
	s.doc = seedDocument(s.nowMs(), s.cfg.CycleMs)
	if err := s.persist(); err != nil {
		s.doc = before
		return err
	}
	return nil
}

func (s *FileStore) Close() error { return nil }

// windowIndex must be called with the lock held.
func (s *FileStore) windowIndex(id string) int {
	for i := range s.doc.Windows {
		if s.doc.Windows[i].ID == id {
			return i
		}
	}
	return -1
}

// findMedia must be called with the lock held.
func (s *FileStore) findMedia(id string) (models.Media, bool) {
	for _, m := range s.doc.Media {
		if m.ID == id {
			return m, true
		}
	}
	return models.Media{}, false
}

func (s *FileStore) copyWindow(w models.Window) models.Window {
	// Always hand back a non-nil slice: a window with an empty playlist must
	// serialise as "items": [] rather than "items": null, so clients can
	// iterate the field without a null check.
	items := make([]models.PlaylistItem, len(w.Items))
	copy(items, w.Items)
	w.Items = items
	return w
}

// newID returns a short, collision-safe identifier such as "md_9f2c1a7b".
func newID(prefix string) string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing is fatal for uniqueness; fall back to the clock.
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}

// Package schedule turns wall-clock time into "what should this window be
// showing right now".
//
// The scheduler is a pure function of (window, sync event, time). Nothing is
// stored about playback progress, and no timer decides when the next item
// starts. A player that joins late, reloads the page or sleeps for an hour
// resolves the same frame as every other player, which is what makes the sync
// takeover land on all windows at the same instant.
//
// Timeline model for one window:
//
//	|<---------------------- cycle (5h) ---------------------->|<-- next cycle
//	[ loop ][ loop ][ loop ] ...                    [ loop ][cut]
//	 ^item0                                                  ^ truncated at the
//	                                                           cycle boundary,
//	                                                           then item0 again
package schedule

import "media-sequencer/internal/models"

// Source says where the resolved frame came from.
type Source string

const (
	// SourceSync means a sync takeover is on screen in every window.
	SourceSync Source = "sync"
	// SourcePlaylist means the window is running its own sequence.
	SourcePlaylist Source = "playlist"
	// SourceIdle means the window has nothing to play (empty playlist).
	SourceIdle Source = "idle"
)

// idleRecheckMs is how long an idle frame stays valid before the player asks
// again. It only matters for empty playlists.
const idleRecheckMs int64 = 1000

// Frame is one resolved slot of playback.
type Frame struct {
	Source  Source `json:"source"`
	MediaID string `json:"mediaId"`
	// ItemID and ItemIndex are empty/-1 for sync and idle frames.
	ItemID    string `json:"itemId"`
	ItemIndex int    `json:"itemIndex"`
	// StartAtMs may be in the past: it is when this frame began, not when it
	// was resolved. Players use (now - StartAtMs) to seek video into position.
	StartAtMs int64 `json:"startAtMs"`
	// EndAtMs is the first instant the frame is no longer valid.
	EndAtMs int64 `json:"endAtMs"`
	// CycleElapsedMs is the position inside the 5-hour cycle, for progress UI.
	CycleElapsedMs int64 `json:"cycleElapsedMs"`
	// LoopIndex counts how many complete passes through the playlist have
	// happened inside the current cycle.
	LoopIndex int64 `json:"loopIndex"`
}

// DurationMs is the length of the frame.
func (f Frame) DurationMs() int64 { return f.EndAtMs - f.StartAtMs }

// floorMod is a modulo that stays non-negative for times before the anchor.
func floorMod(a, m int64) int64 {
	if m <= 0 {
		return 0
	}
	r := a % m
	if r < 0 {
		r += m
	}
	return r
}

// Resolve returns the frame a window must display at nowMs.
//
// An active sync wins over the playlist, but the playlist timeline keeps
// running underneath: the window is not paused, so when the sync ends the
// window continues exactly where its own sequence had got to. That is what
// "continue its own normal sequence without losing its playlist configuration"
// means here, and it keeps windows from drifting apart after repeated syncs.
func Resolve(w models.Window, sync *models.SyncEvent, nowMs int64) Frame {
	if sync != nil && sync.ActiveAt(nowMs) {
		return Frame{
			Source:         SourceSync,
			MediaID:        sync.MediaID,
			ItemIndex:      -1,
			StartAtMs:      sync.StartAtMs,
			EndAtMs:        sync.EndAtMs(),
			CycleElapsedMs: cycleElapsed(w, nowMs),
			LoopIndex:      -1,
		}
	}
	return resolvePlaylist(w, nowMs)
}

// ResolveNext returns the frame that follows the one playing at nowMs. Players
// use it to preload the next image or video so there is no gap between items.
func ResolveNext(w models.Window, sync *models.SyncEvent, nowMs int64) Frame {
	current := Resolve(w, sync, nowMs)
	next := current.EndAtMs
	if next <= nowMs {
		next = nowMs + 1
	}
	return Resolve(w, sync, next)
}

// cycleElapsed is the position inside the current cycle, always >= 0.
func cycleElapsed(w models.Window, nowMs int64) int64 {
	cycle := effectiveCycleMs(w)
	if cycle <= 0 {
		return 0
	}
	return floorMod(nowMs-w.AnchorMs, cycle)
}

// effectiveCycleMs falls back to one playlist pass if no cycle is configured,
// so a misconfigured window still loops instead of dividing by zero.
func effectiveCycleMs(w models.Window) int64 {
	if w.CycleMs > 0 {
		return w.CycleMs
	}
	return w.LoopMs()
}

func resolvePlaylist(w models.Window, nowMs int64) Frame {
	loop := w.LoopMs()
	if loop <= 0 {
		// Empty playlist. The window shows its fallback state; it does not
		// borrow media from anywhere else.
		return Frame{
			Source:    SourceIdle,
			ItemIndex: -1,
			StartAtMs: nowMs,
			EndAtMs:   nowMs + idleRecheckMs,
		}
	}

	cycle := effectiveCycleMs(w)
	elapsed := floorMod(nowMs-w.AnchorMs, cycle)
	cycleStart := nowMs - elapsed
	cycleEnd := cycleStart + cycle

	loopIndex := elapsed / loop
	loopStart := cycleStart + loopIndex*loop
	pos := elapsed - loopIndex*loop

	var cum int64
	for i, item := range w.Items {
		if item.DurationMs <= 0 {
			continue
		}
		if pos < cum+item.DurationMs {
			start := loopStart + cum
			end := start + item.DurationMs
			// The cycle boundary cuts the item short and playback restarts
			// from the first item. Without this clip the 5-hour cycle would
			// slowly slide, because a cycle is rarely a whole number of loops.
			if end > cycleEnd {
				end = cycleEnd
			}
			return Frame{
				Source:         SourcePlaylist,
				MediaID:        item.MediaID,
				ItemID:         item.ID,
				ItemIndex:      i,
				StartAtMs:      start,
				EndAtMs:        end,
				CycleElapsedMs: elapsed,
				LoopIndex:      loopIndex,
			}
		}
		cum += item.DurationMs
	}

	// Unreachable while pos < loop, but stay defensive rather than panic in a
	// display that is meant to run for hours.
	return Frame{
		Source:         SourceIdle,
		ItemIndex:      -1,
		StartAtMs:      nowMs,
		EndAtMs:        nowMs + idleRecheckMs,
		CycleElapsedMs: elapsed,
		LoopIndex:      loopIndex,
	}
}

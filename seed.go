package store

import "media-sequencer/internal/models"

// Seed IDs are fixed rather than random so the README examples, the API docs
// and a freshly seeded database always line up.
const (
	MediaM1    = "md_m1"
	MediaM2    = "md_m2"
	MediaM3    = "md_m3"
	MediaM4    = "md_m4"
	MediaM5    = "md_m5"
	MediaM6    = "md_m6"
	MediaV1    = "md_v1"
	MediaV2    = "md_v2"
	MediaBlank = "md_blank"
)

// Sample videos are public test files. Swap the URLs for your own media by
// editing this file or by adding media through POST /api/media at runtime.
const (
	sampleVideoA = "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/ForBiggerBlazes.mp4"
	sampleVideoB = "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/ForBiggerJoyrides.mp4"
)

// seedDocument builds the starting state: a shared library and four windows
// whose playlists deliberately differ in length, so the windows fall out of
// phase with each other and a sync takeover is obvious on screen.
//
// Image URLs are relative because the backend serves those files itself from
// the embedded assets directory. Any absolute http(s) URL works too.
func seedDocument(nowMs, cycleMs int64) document {
	media := []models.Media{
		{ID: MediaM1, Label: "M1", Type: models.MediaImage, URL: "/media/m1.svg", DurationMs: 6000, CreatedAtMs: nowMs},
		{ID: MediaM2, Label: "M2", Type: models.MediaImage, URL: "/media/m2.svg", DurationMs: 6000, CreatedAtMs: nowMs},
		{ID: MediaM3, Label: "M3", Type: models.MediaImage, URL: "/media/m3.svg", DurationMs: 6000, CreatedAtMs: nowMs},
		{ID: MediaM4, Label: "M4", Type: models.MediaImage, URL: "/media/m4.svg", DurationMs: 6000, CreatedAtMs: nowMs},
		{ID: MediaM5, Label: "M5", Type: models.MediaImage, URL: "/media/m5.svg", DurationMs: 6000, CreatedAtMs: nowMs},
		{ID: MediaM6, Label: "M6", Type: models.MediaImage, URL: "/media/m6.svg", DurationMs: 6000, CreatedAtMs: nowMs},
		{ID: MediaV1, Label: "V1", Type: models.MediaVideo, URL: sampleVideoA, DurationMs: 12000, CreatedAtMs: nowMs},
		{ID: MediaV2, Label: "V2", Type: models.MediaVideo, URL: sampleVideoB, DurationMs: 12000, CreatedAtMs: nowMs},
		{ID: MediaBlank, Label: "Blank", Type: models.MediaBlank, URL: "", DurationMs: 4000, CreatedAtMs: nowMs},
	}

	windows := []models.Window{
		newSeedWindow("wn_lobby", "Lobby wall", nowMs, cycleMs,
			seedItem("it_lobby_1", MediaM1, 6000, nowMs),
			seedItem("it_lobby_2", MediaM2, 6000, nowMs),
			seedItem("it_lobby_3", MediaM3, 6000, nowMs),
		),
		newSeedWindow("wn_corridor", "Corridor A", nowMs, cycleMs,
			seedItem("it_corridor_1", MediaM2, 5000, nowMs),
			seedItem("it_corridor_2", MediaM4, 5000, nowMs),
			seedItem("it_corridor_3", MediaV1, 12000, nowMs),
			seedItem("it_corridor_4", MediaBlank, 4000, nowMs),
		),
		newSeedWindow("wn_cafeteria", "Cafeteria", nowMs, cycleMs,
			seedItem("it_cafeteria_1", MediaM5, 7000, nowMs),
			seedItem("it_cafeteria_2", MediaM6, 7000, nowMs),
			seedItem("it_cafeteria_3", MediaM1, 7000, nowMs),
		),
		newSeedWindow("wn_reception", "Reception", nowMs, cycleMs,
			seedItem("it_reception_1", MediaM3, 8000, nowMs),
			seedItem("it_reception_2", MediaV2, 12000, nowMs),
			seedItem("it_reception_3", MediaM6, 8000, nowMs),
		),
	}

	return document{
		Version: documentVersion,
		Windows: windows,
		Media:   media,
		Sync:    nil,
		SavedAt: nowMs,
	}
}

func newSeedWindow(id, name string, nowMs, cycleMs int64, items ...models.PlaylistItem) models.Window {
	return models.Window{
		ID:          id,
		Name:        name,
		Items:       items,
		AnchorMs:    nowMs,
		CycleMs:     cycleMs,
		Version:     1,
		UpdatedAtMs: nowMs,
	}
}

func seedItem(id, mediaID string, durationMs, nowMs int64) models.PlaylistItem {
	return models.PlaylistItem{ID: id, MediaID: mediaID, DurationMs: durationMs, AddedAtMs: nowMs}
}

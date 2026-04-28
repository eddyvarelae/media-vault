// Package importer ingests video-tagger reports (and similar
// JSON-per-file metadata producers) into the vault manifest.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// VideoTaggerReport mirrors the JSON shape video-tagger writes.
type VideoTaggerReport struct {
	File     string         `json:"file"`
	AITags   []string       `json:"ai_tags"`
	UserTags []string       `json:"user_tags"`
	Metadata map[string]any `json:"metadata"`
	Transcript struct {
		Language string `json:"language"`
		FullText string `json:"full_text"`
	} `json:"transcript"`
	Frames []map[string]any `json:"frames"`
}

type Result struct {
	Reports         int
	Matched         int
	Ambiguous       int
	NotFound        int
	TagsApplied     int
	MetadataWritten int
}

// Run walks reportsDir for `report.json` files and ingests each into `disk`.
// For each report:
//   1. Look up the file by basename within `disk`. Skip if 0 or >1 matches.
//   2. Apply each ai_tag via the manifest tag system.
//   3. Store transcript, language, scene description, EXIF metadata as
//      structured key/value rows in the metadata table.
func Run(ctx context.Context, m *manifest.Manifest, disk, reportsDir string, onFile func(report, status string)) (*Result, error) {
	res := &Result{}

	err := filepath.WalkDir(reportsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || d.Name() != "report.json" {
			return nil
		}
		res.Reports++

		raw, err := os.ReadFile(path)
		if err != nil {
			if onFile != nil {
				onFile(path, "read-error: "+err.Error())
			}
			return nil
		}
		var rep VideoTaggerReport
		if err := json.Unmarshal(raw, &rep); err != nil {
			if onFile != nil {
				onFile(path, "json-error: "+err.Error())
			}
			return nil
		}
		if rep.File == "" {
			if onFile != nil {
				onFile(path, "missing-file-field")
			}
			return nil
		}

		hits, err := m.FindByBasename(disk, rep.File)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			res.NotFound++
			if onFile != nil {
				onFile(rep.File, "not-found-in-manifest")
			}
			return nil
		}
		if len(hits) > 1 {
			res.Ambiguous++
			if onFile != nil {
				onFile(rep.File, fmt.Sprintf("ambiguous (%d matches)", len(hits)))
			}
			return nil
		}
		entry := hits[0]
		res.Matched++

		// Apply AI tags
		for _, tag := range rep.AITags {
			if tag == "" {
				continue
			}
			n, err := m.ApplyTag(disk, entry.SourcePath, tag)
			if err != nil {
				if onFile != nil {
					onFile(rep.File, "tag-error: "+err.Error())
				}
				continue
			}
			res.TagsApplied += int(n)
		}

		// Store structured metadata
		write := func(key, value string) {
			if value == "" {
				return
			}
			if err := m.SetMetadata(disk, entry.SourcePath, key, value); err != nil {
				if onFile != nil {
					onFile(rep.File, "metadata-error: "+err.Error())
				}
				return
			}
			res.MetadataWritten++
		}
		if rep.Transcript.FullText != "" {
			write("transcript", rep.Transcript.FullText)
			write("transcript_language", rep.Transcript.Language)
		}
		if rep.Metadata != nil {
			if v, ok := rep.Metadata["date"].(string); ok {
				write("captured_at", v)
			}
			if v, ok := rep.Metadata["camera"].(string); ok {
				write("camera", v)
			}
			if v, ok := rep.Metadata["location"].(string); ok && v != "" {
				write("location", v)
			}
			if g, ok := rep.Metadata["gps"].(map[string]any); ok && g != nil {
				if blob, err := json.Marshal(g); err == nil {
					write("gps", string(blob))
				}
			}
		}
		// Concatenate per-frame scene descriptions for searchability.
		var scenes []string
		for _, f := range rep.Frames {
			if reasoning, ok := f["reasoning"].(string); ok && reasoning != "" {
				ts, _ := f["timestamp"].(string)
				if ts != "" {
					scenes = append(scenes, "["+ts+"] "+reasoning)
				} else {
					scenes = append(scenes, reasoning)
				}
			}
		}
		if len(scenes) > 0 {
			write("scene_descriptions", strings.Join(scenes, "\n"))
		}

		if onFile != nil {
			onFile(rep.File, fmt.Sprintf("ingested (%d tags)", len(rep.AITags)))
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

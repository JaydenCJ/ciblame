// JSON renderers. Every document carries {"tool": "ciblame",
// "schema_version": 1, "kind": …} so downstream scripts can dispatch and
// version-check before parsing further. Durations are seconds at
// millisecond precision; timestamps are RFC 3339 UTC.
package render

import (
	"encoding/json"
	"io"
	"math"
	"time"

	"github.com/JaydenCJ/ciblame/internal/diffrun"
	"github.com/JaydenCJ/ciblame/internal/run"
	"github.com/JaydenCJ/ciblame/internal/timeline"
)

// SchemaVersion identifies the JSON output contract. It only changes on
// breaking shape changes, never for added fields.
const SchemaVersion = 1

type envelope struct {
	Tool          string `json:"tool"`
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
}

func newEnvelope(kind string) envelope {
	return envelope{Tool: "ciblame", SchemaVersion: SchemaVersion, Kind: kind}
}

type jsonGroup struct {
	Title     string  `json:"title"`
	DurationS float64 `json:"duration_s"`
}

type jsonStep struct {
	Number    int         `json:"number"`
	Name      string      `json:"name"`
	Start     *string     `json:"start"`
	End       *string     `json:"end"`
	DurationS float64     `json:"duration_s"`
	OffsetS   float64     `json:"offset_s"`
	GapS      float64     `json:"gap_s"`
	Lines     int         `json:"lines"`
	Errors    int         `json:"errors"`
	Warnings  int         `json:"warnings"`
	Commands  int         `json:"commands"`
	Failed    bool        `json:"failed"`
	Groups    []jsonGroup `json:"groups,omitempty"`
}

type jsonJob struct {
	Name          string     `json:"name"`
	Index         int        `json:"index"`
	DurationS     float64    `json:"duration_s"`
	OverheadS     float64    `json:"overhead_s"`
	Start         *string    `json:"start"`
	End           *string    `json:"end"`
	Failed        bool       `json:"failed"`
	LogOnly       bool       `json:"log_only"`
	RunnerVersion string     `json:"runner_version,omitempty"`
	RunnerImage   string     `json:"runner_image,omitempty"`
	Steps         []jsonStep `json:"steps"`
}

type jsonReport struct {
	envelope
	Archive  string    `json:"archive"`
	Jobs     []jsonJob `json:"jobs"`
	JobTimeS float64   `json:"job_time_s"`
	WallS    float64   `json:"wall_s"`
	Steps    int       `json:"steps"`
	Skipped  int       `json:"skipped_entries"`
}

func stamp(t time.Time, timed bool) *string {
	if !timed {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

// JSON writes the report document for a run.
func JSON(w io.Writer, r *run.Run) error {
	doc := jsonReport{
		envelope: newEnvelope("report"),
		Archive:  r.Label,
		JobTimeS: Seconds(r.JobTime()),
		WallS:    Seconds(r.Wall()),
		Steps:    r.StepCount(),
		Skipped:  r.Skipped,
	}
	for _, j := range r.Jobs {
		jj := jsonJob{
			Name:          j.Name,
			Index:         j.Index,
			DurationS:     Seconds(j.Duration()),
			OverheadS:     Seconds(timeline.Overhead(j)),
			Start:         stamp(j.Start, j.Timed),
			End:           stamp(j.End, j.Timed),
			Failed:        j.Failed,
			LogOnly:       j.LogOnly,
			RunnerVersion: j.RunnerVer,
			RunnerImage:   j.Image,
			Steps:         []jsonStep{},
		}
		for _, sp := range timeline.Spans(j) {
			s := sp.Step
			js := jsonStep{
				Number:    s.Number,
				Name:      s.Name,
				Start:     stamp(s.Start, s.Timed),
				End:       stamp(s.End, s.Timed),
				DurationS: Seconds(s.Duration()),
				OffsetS:   Seconds(sp.Offset),
				GapS:      Seconds(sp.Gap),
				Lines:     s.Lines,
				Errors:    s.Errors,
				Warnings:  s.Warnings,
				Commands:  s.Commands,
				Failed:    s.Failed,
			}
			for _, g := range s.Groups {
				js.Groups = append(js.Groups, jsonGroup{Title: g.Title, DurationS: Seconds(g.Duration())})
			}
			jj.Steps = append(jj.Steps, js)
		}
		doc.Jobs = append(doc.Jobs, jj)
	}
	return encode(w, doc)
}

type jsonSlowEntry struct {
	Job       string  `json:"job"`
	Step      string  `json:"step"`
	Number    int     `json:"number"`
	DurationS float64 `json:"duration_s"`
	SharePct  float64 `json:"share_pct"`
	Failed    bool    `json:"failed"`
}

type jsonSlow struct {
	envelope
	Archive  string          `json:"archive"`
	JobTimeS float64         `json:"job_time_s"`
	Steps    []jsonSlowEntry `json:"steps"`
}

// SlowJSON writes the slowest-steps ranking as JSON.
func SlowJSON(w io.Writer, r *run.Run, entries []timeline.Slow) error {
	doc := jsonSlow{
		envelope: newEnvelope("slow"),
		Archive:  r.Label,
		JobTimeS: Seconds(r.JobTime()),
		Steps:    []jsonSlowEntry{},
	}
	for _, e := range entries {
		doc.Steps = append(doc.Steps, jsonSlowEntry{
			Job:       e.Job.Name,
			Step:      e.Step.Name,
			Number:    e.Step.Number,
			DurationS: Seconds(e.Step.Duration()),
			SharePct:  round1(e.Share),
			Failed:    e.Step.Failed,
		})
	}
	return encode(w, doc)
}

type jsonStepDiff struct {
	Name   string  `json:"name"`
	Status string  `json:"status"`
	BaseS  float64 `json:"base_s"`
	HeadS  float64 `json:"head_s"`
	DeltaS float64 `json:"delta_s"`
}

type jsonJobDiff struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	BaseS  float64        `json:"base_s"`
	HeadS  float64        `json:"head_s"`
	DeltaS float64        `json:"delta_s"`
	Steps  []jsonStepDiff `json:"steps"`
}

type jsonDiff struct {
	envelope
	Base          string        `json:"base"`
	Head          string        `json:"head"`
	BaseJobTimeS  float64       `json:"base_job_time_s"`
	HeadJobTimeS  float64       `json:"head_job_time_s"`
	JobTimeDeltaS float64       `json:"job_time_delta_s"`
	BaseWallS     float64       `json:"base_wall_s"`
	HeadWallS     float64       `json:"head_wall_s"`
	WallDeltaS    float64       `json:"wall_delta_s"`
	Jobs          []jsonJobDiff `json:"jobs"`
}

// DiffJSON writes the two-run comparison as JSON. minDelta is not applied:
// machines get every step and filter themselves.
func DiffJSON(w io.Writer, rep *diffrun.Report) error {
	doc := jsonDiff{
		envelope:      newEnvelope("diff"),
		Base:          rep.BaseLabel,
		Head:          rep.HeadLabel,
		BaseJobTimeS:  Seconds(rep.BaseJobTime),
		HeadJobTimeS:  Seconds(rep.HeadJobTime),
		JobTimeDeltaS: Seconds(rep.JobTimeDelta()),
		BaseWallS:     Seconds(rep.BaseWall),
		HeadWallS:     Seconds(rep.HeadWall),
		WallDeltaS:    Seconds(rep.WallDelta()),
		Jobs:          []jsonJobDiff{},
	}
	for _, jd := range rep.Jobs {
		jj := jsonJobDiff{
			Name:   jd.Name,
			Status: jd.Kind.String(),
			BaseS:  Seconds(jd.Base),
			HeadS:  Seconds(jd.Head),
			DeltaS: Seconds(jd.Delta()),
			Steps:  []jsonStepDiff{},
		}
		for _, sd := range jd.Steps {
			jj.Steps = append(jj.Steps, jsonStepDiff{
				Name:   sd.Name,
				Status: sd.Kind.String(),
				BaseS:  Seconds(sd.Base),
				HeadS:  Seconds(sd.Head),
				DeltaS: Seconds(sd.Delta()),
			})
		}
		doc.Jobs = append(doc.Jobs, jj)
	}
	return encode(w, doc)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func encode(w io.Writer, doc any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

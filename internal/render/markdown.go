// Markdown renderers, shaped for pasting into pull-request comments: one
// table per job for reports, one table per job for diffs, biggest movers
// first. Pipes in step names are escaped so tables never break.
package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JaydenCJ/ciblame/internal/diffrun"
	"github.com/JaydenCJ/ciblame/internal/run"
	"github.com/JaydenCJ/ciblame/internal/timeline"
)

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// Markdown writes the waterfall report as PR-ready Markdown.
func Markdown(w io.Writer, r *run.Run) {
	fmt.Fprintf(w, "## ciblame report — %s\n\n", cell(r.Label))
	fmt.Fprintf(w, "%s · %s · wall **%s** · job time **%s**\n",
		Count(len(r.Jobs), "job"), Count(r.StepCount(), "step"), Dur(r.Wall()), Dur(r.JobTime()))
	for _, j := range r.Jobs {
		fmt.Fprintf(w, "\n### %s — %s\n\n", cell(j.Name), Dur(j.Duration()))
		if j.LogOnly {
			fmt.Fprintf(w, "_Combined job log only — no per-step files in this archive._\n")
			continue
		}
		fmt.Fprintf(w, "| # | step | start | dur | share |\n")
		fmt.Fprintf(w, "|--:|---|--:|--:|--:|\n")
		total := j.Duration()
		for _, sp := range timeline.Spans(j) {
			s := sp.Step
			share := "—"
			if total > 0 && s.Timed {
				share = Pct(float64(s.Duration()) / float64(total) * 100)
			}
			name := cell(s.Name)
			if s.Failed {
				name += " ✗"
			}
			fmt.Fprintf(w, "| %d | %s | %s | %s | %s |\n",
				s.Number, name, SignedDur(sp.Offset), Dur(s.Duration()), share)
		}
		if oh := timeline.Overhead(j); oh > 0 {
			fmt.Fprintf(w, "\n_Between-step overhead: %s._\n", Dur(oh))
		}
	}
}

// DiffMarkdown writes the two-run comparison as PR-ready Markdown,
// folding steps below minDelta into a per-job footnote.
func DiffMarkdown(w io.Writer, rep *diffrun.Report, minDelta time.Duration) {
	fmt.Fprintf(w, "## ciblame diff — %s → %s\n\n", cell(rep.BaseLabel), cell(rep.HeadLabel))
	fmt.Fprintf(w, "| | base | head | delta |\n|---|--:|--:|--:|\n")
	fmt.Fprintf(w, "| job time | %s | %s | **%s** |\n",
		Dur(rep.BaseJobTime), Dur(rep.HeadJobTime), SignedDur(rep.JobTimeDelta()))
	fmt.Fprintf(w, "| wall | %s | %s | %s |\n",
		Dur(rep.BaseWall), Dur(rep.HeadWall), SignedDur(rep.WallDelta()))
	for i := range rep.Jobs {
		jd := &rep.Jobs[i]
		title := fmt.Sprintf("%s — %s", cell(jd.Name), SignedDur(jd.Delta()))
		if jd.Kind != diffrun.Matched {
			title += fmt.Sprintf(" (job %s)", jd.Kind)
		}
		fmt.Fprintf(w, "\n### %s\n\n", title)
		shown, hidden, hiddenNet := diffrun.Fold(jd.Steps, minDelta)
		if len(shown) > 0 {
			fmt.Fprintf(w, "| step | base | head | delta |\n|---|--:|--:|--:|\n")
			for _, sd := range shown {
				fmt.Fprintf(w, "| %s %s | %s | %s | %s |\n",
					kindSigil(sd.Kind), cell(sd.Name),
					sideDur(sd, false), sideDur(sd, true), SignedDur(sd.Delta()))
			}
		}
		if hidden > 0 {
			fmt.Fprintf(w, "\n_%s within ±%s (net %s)._\n",
				Count(hidden, "step"), Dur(minDelta), SignedDur(hiddenNet))
		}
	}
}

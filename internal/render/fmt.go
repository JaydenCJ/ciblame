// Shared formatting helpers for the human-facing renderers. All duration
// formatting funnels through Dur/SignedDur so text, markdown, and diff
// output agree on every number.
package render

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// Dur formats a duration for humans: tenths of a second below a minute
// ("8.5s"), minute+second above ("3m42s"), hour form beyond ("1h02m03s").
func Dur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
}

// SignedDur formats a delta with an explicit sign: "+3m56s", "-0.5s".
// Exactly zero renders as "+0.0s" — a delta, not an absence.
func SignedDur(d time.Duration) string {
	if d < 0 {
		return "-" + Dur(-d)
	}
	return "+" + Dur(d)
}

// Count formats a number with a singular/plural noun: "1 job", "3 steps".
func Count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Pct formats a percentage to one decimal.
func Pct(v float64) string { return fmt.Sprintf("%.1f%%", v) }

// Clock formats a timestamp as a compact UTC wall-clock, for job headers.
func Clock(t time.Time) string { return t.UTC().Format("15:04:05Z") }

// Seconds rounds a duration to millisecond precision as a float, the JSON
// representation of every duration ciblame emits.
func Seconds(d time.Duration) float64 {
	return float64(d.Round(time.Millisecond)) / float64(time.Second)
}

// Truncate shortens s to max runes, ending in an ellipsis when it cuts.
func Truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// Pad right-pads s with spaces to width display runes.
func Pad(s string, width int) string {
	n := utf8.RuneCountInString(s)
	for n < width {
		s += " "
		n++
	}
	return s
}

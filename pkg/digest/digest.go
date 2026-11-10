// Package digest renders a SessionAnalysis as compact, size-bounded text
// for an LLM to summarise: a header (models, time, tokens, cost, tool mix),
// then the episodes in order (the user's ask, the tools used, files touched,
// key assistant statements, errors and retries), then the final outcome.
//
// The output never exceeds Options.MaxBytes. When the full rendering is too
// big, detail is reduced step by step (shorter asks and statements, then one
// line per episode, then the middle episodes are elided) before a final
// hard cut.
package digest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/geekychris/claude-session-analyzer/pkg/analyze"
	"github.com/geekychris/claude-session-analyzer/pkg/toolclass"
)

// DefaultMaxBytes is used when Options.MaxBytes is 0.
const DefaultMaxBytes = 16 * 1024

// Options control the digest.
type Options struct {
	MaxBytes int    // budget for the whole digest (default DefaultMaxBytes)
	Title    string // optional first line, e.g. "Session 2 of 3: implement"
}

type level struct {
	ask, statement, statements, errors, files, final int
	oneLine                                          bool
	keep                                             int // episodes kept at each end when eliding; 0 = all
}

var levels = []level{
	{ask: 1200, statement: 500, statements: 6, errors: 4, files: 12, final: 1500},
	{ask: 500, statement: 240, statements: 3, errors: 2, files: 6, final: 800},
	{ask: 240, statement: 160, statements: 2, errors: 1, files: 4, final: 500},
	{ask: 140, files: 3, final: 400, oneLine: true},
	{ask: 100, files: 2, final: 300, oneLine: true, keep: 40},
	{ask: 80, files: 1, final: 240, oneLine: true, keep: 15},
	{ask: 60, final: 200, oneLine: true, keep: 5},
	{ask: 40, final: 120, oneLine: true, keep: 2},
}

// Build renders a within budget.
func Build(a analyze.SessionAnalysis, opts Options) string {
	max := opts.MaxBytes
	if max <= 0 {
		max = DefaultMaxBytes
	}
	var out string
	for _, lv := range levels {
		out = render(a, opts.Title, lv)
		if len(out) <= max {
			return out
		}
	}
	return Truncate(out, max)
}

// Truncate cuts s to at most n bytes on a rune boundary, marking the cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const mark = "\n…[truncated]\n"
	if n <= len(mark) {
		return cut(s, n)
	}
	return cut(s, n-len(mark)) + mark
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n <= 0 || len(s) <= n {
		return s
	}
	return cut(s, n) + "…"
}

func render(a analyze.SessionAnalysis, title string, lv level) string {
	var b strings.Builder
	if title != "" {
		b.WriteString(title)
		b.WriteString("\n")
	}
	writeHeader(&b, a, lv)
	n := len(a.Episodes)
	fmt.Fprintf(&b, "\nEPISODES (%d; an episode = one user ask until the first edit and the work after it)\n", n)
	for i, e := range a.Episodes {
		if lv.keep > 0 && n > 2*lv.keep && i == lv.keep {
			fmt.Fprintf(&b, "… %d episodes omitted …\n", n-2*lv.keep)
		}
		if lv.keep > 0 && n > 2*lv.keep && i >= lv.keep && i < n-lv.keep {
			continue
		}
		if lv.oneLine {
			writeEpisodeLine(&b, e, lv)
		} else {
			writeEpisode(&b, a, e, lv)
		}
	}
	if a.FinalText != "" {
		b.WriteString("\nFINAL OUTCOME (last assistant message)\n")
		b.WriteString(clip(a.FinalText, lv.final))
		b.WriteString("\n")
	}
	return b.String()
}

func writeHeader(b *strings.Builder, a analyze.SessionAnalysis, lv level) {
	b.WriteString("SESSION")
	if a.SessionID != "" {
		b.WriteString(" " + a.SessionID)
	}
	b.WriteString("\n")
	if a.Title != "" {
		fmt.Fprintf(b, "Title: %s\n", clip(a.Title, 200))
	}
	if a.CWD != "" {
		fmt.Fprintf(b, "Directory: %s", a.CWD)
		if a.GitBranch != "" {
			fmt.Fprintf(b, " (branch %s)", a.GitBranch)
		}
		b.WriteString("\n")
	}
	if a.FirstTimestamp != "" {
		fmt.Fprintf(b, "Time: %s → %s; wall %s, active %s\n", a.FirstTimestamp, a.LastTimestamp,
			Dur(a.WallSeconds), Dur(a.ActiveSeconds))
	}
	var models []string
	for _, m := range a.Models {
		models = append(models, m.Model)
	}
	if len(models) > 0 {
		fmt.Fprintf(b, "Models: %s\n", strings.Join(models, ", "))
	}
	fmt.Fprintf(b, "Tokens: in %d, out %d, cache read %d, cache write %d; est. cost $%.2f\n",
		a.Tokens.Input, a.Tokens.Output, a.Tokens.CacheRead, a.Tokens.CacheCreation, a.CostUSD)
	fmt.Fprintf(b, "Turns: %d user asks, %d assistant responses, %d tool calls (%s)\n",
		a.UserTurns, a.AssistantMessages, len(a.ToolCalls), CategoryMix(a.ToolCounts))
	if a.Subagents.Calls > 0 {
		fmt.Fprintf(b, "Sub-agents: %d calls", a.Subagents.Calls)
		if len(a.Subagents.Tasks) > 0 {
			k := min(len(a.Subagents.Tasks), max(1, lv.files/2))
			var ts []string
			for _, t := range a.Subagents.Tasks[:k] {
				ts = append(ts, clip(t, 80))
			}
			fmt.Fprintf(b, " (e.g. %s)", strings.Join(ts, "; "))
		}
		b.WriteString("\n")
	}
	if len(a.Errors) > 0 || a.Retries > 0 {
		fmt.Fprintf(b, "Errors: %d, retries after an error: %d\n", len(a.Errors), a.Retries)
	}
	if edited := a.FilesEdited(); len(edited) > 0 {
		fmt.Fprintf(b, "Files edited (%d): %s\n", len(edited), listN(edited, max(lv.files, 3)))
	}
	if len(a.PRLinks) > 0 {
		fmt.Fprintf(b, "Pull requests: %s\n", strings.Join(a.PRLinks, ", "))
	}
}

func writeEpisode(b *strings.Builder, a analyze.SessionAnalysis, e analyze.Episode, lv level) {
	fmt.Fprintf(b, "\n#%d", e.Seq)
	if e.StartedAt != "" {
		fmt.Fprintf(b, " at %s", e.StartedAt)
	}
	if e.DurationMs > 0 {
		fmt.Fprintf(b, " (%s to %s)", Dur(float64(e.DurationMs)/1000), map[bool]string{true: "first edit", false: "end"}[e.Ended])
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "Ask: %s\n", clip(e.UserPrompt, lv.ask))
	if e.Ended {
		fmt.Fprintf(b, "Tools before first edit: %d\n", e.ToolsBeforeEdit)
	} else {
		fmt.Fprintf(b, "No edit in this episode; tools: %d\n", len(e.ToolCallIDs))
	}
	if len(e.ToolNames) > 0 {
		fmt.Fprintf(b, "Tools used: %s\n", NameCounts(e.ToolNames, 10))
	}
	if len(e.FilesEdited) > 0 {
		fmt.Fprintf(b, "Edited: %s\n", listN(e.FilesEdited, lv.files))
	}
	if read := readOnly(e); len(read) > 0 {
		fmt.Fprintf(b, "Read: %s\n", listN(read, lv.files))
	}
	if cmds := bashCommands(a, e, lv.files); len(cmds) > 0 {
		fmt.Fprintf(b, "Commands: %s\n", strings.Join(cmds, "; "))
	}
	if e.Errors > 0 {
		fmt.Fprintf(b, "Errors: %d (retries %d)\n", e.Errors, e.Retries)
		k := 0
		for _, id := range e.ToolCallIDs {
			tc := a.ToolCalls[id]
			if !tc.IsError || k >= lv.errors {
				continue
			}
			k++
			fmt.Fprintf(b, "  - %s %s: %s\n", tc.Name, clip(tc.Summary, 80), clip(tc.ErrorText, 160))
		}
	}
	st := e.Statements
	if len(st) > lv.statements {
		head := lv.statements / 2
		tail := lv.statements - head
		st = append(append(append([]string{}, st[:head]...), "…"), st[len(st)-tail:]...)
	}
	for _, s := range st {
		if s == "…" {
			b.WriteString("  …\n")
			continue
		}
		fmt.Fprintf(b, "  > %s\n", clip(s, lv.statement))
	}
}

func writeEpisodeLine(b *strings.Builder, e analyze.Episode, lv level) {
	fmt.Fprintf(b, "#%d %s | tools %d (%s)", e.Seq, clip(e.UserPrompt, lv.ask), len(e.ToolCallIDs), CategoryMix(e.ToolCounts))
	if len(e.FilesEdited) > 0 && lv.files > 0 {
		fmt.Fprintf(b, " | edited %s", listN(e.FilesEdited, lv.files))
	} else if len(e.FilesEdited) > 0 {
		fmt.Fprintf(b, " | edited %d files", len(e.FilesEdited))
	}
	if e.Errors > 0 {
		fmt.Fprintf(b, " | errors %d, retries %d", e.Errors, e.Retries)
	}
	b.WriteString("\n")
}

func readOnly(e analyze.Episode) []string {
	ed := map[string]bool{}
	for _, f := range e.FilesEdited {
		ed[f] = true
	}
	var out []string
	for _, f := range e.FilesRead {
		if !ed[f] {
			out = append(out, f)
		}
	}
	return out
}

func bashCommands(a analyze.SessionAnalysis, e analyze.Episode, n int) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range e.ToolCallIDs {
		tc := a.ToolCalls[id]
		if !tc.IsBash || tc.BashProgram == "" {
			continue
		}
		k := strings.TrimSpace(tc.BashProgram + " " + tc.BashAction)
		if seen[k] {
			continue
		}
		seen[k] = true
		if len(out) < n {
			out = append(out, k)
		}
	}
	return out
}

// CategoryMix renders category counts as "read 3, search 1, modify 2".
func CategoryMix(c map[string]int) string {
	var parts []string
	for _, k := range toolclass.Categories {
		if c[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, c[k]))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// NameCounts renders the top n names as "Read×3, Edit×2".
func NameCounts(c map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var xs []kv
	for k, v := range c {
		xs = append(xs, kv{k, v})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].v != xs[j].v {
			return xs[i].v > xs[j].v
		}
		return xs[i].k < xs[j].k
	})
	var parts []string
	for i, x := range xs {
		if i >= n {
			parts = append(parts, fmt.Sprintf("+%d more", len(xs)-n))
			break
		}
		parts = append(parts, fmt.Sprintf("%s×%d", x.k, x.v))
	}
	return strings.Join(parts, ", ")
}

func listN(xs []string, n int) string {
	if n <= 0 {
		n = 1
	}
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(" (+%d more)", len(xs)-n)
}

// Dur renders seconds as "1h02m", "4m10s" or "12s".
func Dur(sec float64) string {
	d := time.Duration(sec * float64(time.Second)).Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

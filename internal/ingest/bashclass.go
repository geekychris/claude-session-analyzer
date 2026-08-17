package ingest

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// actionAwarePrograms are CLIs whose second token is a meaningful verb
// (e.g. `git commit`, `npm install`, `docker build`). For anything else we
// only record the program name; the raw command stays in input_json.
var actionAwarePrograms = map[string]bool{
	"git":     true,
	"gh":      true,
	"npm":     true,
	"yarn":    true,
	"pnpm":    true,
	"docker":  true,
	"kubectl": true,
	"cargo":   true,
	"go":      true,
	"pip":     true,
	"pip3":    true,
	"python":  true,
	"python3": true,
	"node":    true,
	"ruby":    true,
	"mvn":     true,
	"gradle":  true,
	"make":    true,
	"bundle":  true,
	"terraform": true,
	"aws":     true,
	"gcloud":  true,
	"brew":    true,
	"systemctl": true,
	"psql":    true,
	"mysql":   true,
	"sqlite3": true,
	"jq":      true,
}

// ClassifyBashInput extracts the executed program and its (optional)
// subcommand from a Bash tool_use input. The raw command may contain env
// prefixes ("FOO=bar cmd …"), pipelines ("a | b"), and boolean chains
// ("a && b"); we take the leftmost pipeline stage and its first bare token
// as the program.
func ClassifyBashInput(input json.RawMessage) (program, action string) {
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		return "", ""
	}
	cmd, _ := m["command"].(string)
	return classifyBashCommand(cmd)
}

// shellNoiseWords are shell built-ins/keywords that aren't meaningful
// "programs" — we skip past them to find the real command.
var shellNoiseWords = map[string]bool{
	"for": true, "while": true, "until": true, "if": true, "case": true,
	"do": true, "done": true, "then": true, "else": true, "elif": true, "fi": true, "esac": true,
	"function": true, "in": true, "select": true, "time": true,
	// pure variable assignments handled by isEnvAssignment already
}

func classifyBashCommand(cmd string) (program, action string) {
	if cmd == "" {
		return "", ""
	}
	// Multi-line inputs are common: skip comment lines / blanks and pick the
	// first line with a real command.
	if strings.Contains(cmd, "\n") {
		for _, line := range strings.Split(cmd, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// A bare "VAR=…" line is an env var; skip it.
			toks := strings.Fields(line)
			if len(toks) == 1 && isEnvAssignment(toks[0]) {
				continue
			}
			cmd = line
			break
		}
	}
	// Only inspect the first pipeline stage — that's usually the "intent".
	for _, sep := range []string{"&&", "||", ";", "|"} {
		if i := strings.Index(cmd, sep); i > 0 {
			cmd = cmd[:i]
			break
		}
	}
	fields := strings.Fields(cmd)
	// Skip leading env assignments (FOO=bar) and shell keywords.
	for len(fields) > 0 && (isEnvAssignment(fields[0]) || shellNoiseWords[fields[0]]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", ""
	}
	// Comment leftovers (a line starting with `#` that survived above).
	if strings.HasPrefix(fields[0], "#") {
		return "", ""
	}
	program = filepath.Base(strings.TrimSuffix(fields[0], ":"))
	// If the "program" looks like a path/script, keep just its basename and
	// tag as "script" (e.g. /tmp/run_pip.py → run_pip.py / script).
	if strings.HasPrefix(program, "./") || strings.Contains(program, "/") {
		program = filepath.Base(program)
	}
	// Common wrappers: sudo/env/time/xargs → look one level deeper.
	if program == "sudo" || program == "env" || program == "time" || program == "xargs" {
		if len(fields) > 1 {
			inner, act := classifyBashCommand(strings.Join(fields[1:], " "))
			return inner, act
		}
	}
	// Action for well-known CLIs.
	if actionAwarePrograms[program] && len(fields) > 1 {
		next := fields[1]
		if !strings.HasPrefix(next, "-") {
			action = next
		}
	}
	return program, action
}

func isEnvAssignment(s string) bool {
	i := strings.Index(s, "=")
	if i <= 0 {
		return false
	}
	// Very cheap sanity: LHS should look like an identifier.
	for _, r := range s[:i] {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

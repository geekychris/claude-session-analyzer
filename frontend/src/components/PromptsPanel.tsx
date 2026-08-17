import { useMemo, useState } from "react";

// PromptsPanel renders a set of pre-built prompts you can copy and paste into
// Claude. Each prompt is written to invoke the `claude-trace` MCP tools with
// the correct IDs for the current view. If the user hasn't registered the MCP
// server yet, the prompts are still readable and describe the analysis Claude
// should perform.

export type PromptContext =
  | { kind: "dashboard" }
  | { kind: "project"; slug: string; displayName: string; cwd: string }
  | { kind: "session"; sessionId: string; title: string; projectSlug: string }
  | { kind: "episode"; episodeId: number; sessionId: string; projectSlug: string; editFilePath: string; userPrompt: string };

type Prompt = { title: string; body: string; blurb: string };

export function PromptsPanel({ ctx }: { ctx: PromptContext }) {
  const prompts = useMemo(() => buildPrompts(ctx), [ctx]);
  return (
    <section className="prompts">
      <h3>Ask Claude about this</h3>
      <p className="hint">
        Copy any prompt and paste it into a Claude Code session that has the
        <code> claude-trace </code> MCP server registered. Claude will call the
        analyzer's MCP tools and report back. See the README for setup.
      </p>
      <div className="prompt-grid">
        {prompts.map((p) => <PromptCard key={p.title} p={p} />)}
      </div>
    </section>
  );
}

function PromptCard({ p }: { p: Prompt }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(p.body);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Older browsers / restrictive contexts — fall back to a prompt() dialog.
      window.prompt("Copy the prompt below:", p.body);
    }
  }
  return (
    <div className="prompt-card">
      <div className="prompt-head">
        <span className="prompt-title">{p.title}</span>
        <button className="btn small" onClick={copy}>
          {copied ? "copied ✓" : "copy"}
        </button>
      </div>
      <div className="prompt-blurb">{p.blurb}</div>
      <pre className="prompt-body">{p.body}</pre>
    </div>
  );
}

// -----------------------------------------------------------------------------
// Prompt library
// -----------------------------------------------------------------------------

function buildPrompts(ctx: PromptContext): Prompt[] {
  switch (ctx.kind) {
    case "dashboard":
      return [
        {
          title: "Cost & activity roll-up",
          blurb: "One-shot summary across every project.",
          body:
`Use the claude-trace MCP server. Call list_projects and produce a table sorted by estimatedCostUsd: projectDisplayName, sessionCount, totalToolCalls, totalEdits, estimatedCostUsd. Then list the top 5 projects by tool-call-to-edit ratio (high ratio = lots of exploration, few code changes) and hypothesize why.`,
        },
        {
          title: "Global tool usage",
          blurb: "See which tools you actually rely on.",
          body:
`Use the claude-trace MCP server. Call tool_histogram with scope="global" and top_files with scope="global" and limit=25. Summarize: (a) top 10 tools by count, (b) tools with the highest error rate, (c) the 25 hottest files (reads + edits). Highlight anything surprising.`,
        },
        {
          title: "Episode depth distribution",
          blurb: "How much context do I usually build before code?",
          body:
`Use the claude-trace MCP server. Call episode_histogram with scope="global". Interpret the distribution: what fraction of episodes fire an Edit within 5 tool calls versus needing 20+? Speculate about which situations drive the long tail (search-heavy sessions? Big refactors?).`,
        },
      ];

    case "project":
      return [
        {
          title: `Cost & episodes for ${ctx.displayName}`,
          blurb: "Where money and time went in this project.",
          body:
`Use the claude-trace MCP server. Call list_sessions with project_slug="${ctx.slug}". For each session: sessionId, aiTitle, estimatedCostUsd, editCount, episodeCount. Then call tool_histogram with scope="project" id="${ctx.slug}" and top_files with scope="project" id="${ctx.slug}" limit=20. Explain: which sessions were expensive, and did the tool usage justify the spend?`,
        },
        {
          title: "Context vs. code ratio",
          blurb: "Was I mostly exploring or mostly editing?",
          body:
`Use the claude-trace MCP server. For project ${ctx.slug}, call episode_histogram with scope="project" id="${ctx.slug}". Compute what fraction of episodes reached an edit within 5 tool calls vs. required more than 20. If a large fraction needed >20, look at list_sessions for that project and pick the 2 most exploration-heavy sessions — read their session_stats and explain the pattern.`,
        },
        {
          title: "Hot files & possible refactors",
          blurb: "Files edited often are candidates for cleanup.",
          body:
`Use the claude-trace MCP server. Call top_files with scope="project" id="${ctx.slug}" and limit=30. Identify files edited more than 20 times — those are candidates for refactor or better test coverage. For each, hypothesize why they keep changing based on the file path and file type.`,
        },
      ];

    case "session":
      return [
        {
          title: `Full analysis of ${shortTitle(ctx.title)}`,
          blurb: "The single most useful prompt for a session.",
          body:
`Use the claude-trace MCP server. Call get_session id="${ctx.sessionId}" and session_stats id="${ctx.sessionId}" and tool_histogram scope="session" id="${ctx.sessionId}" and top_files scope="session" id="${ctx.sessionId}" limit=20. Then call list_episodes id="${ctx.sessionId}" and sort by toolCallCount descending — show the top 5 with their prompt excerpt and edit target.

Answer:
1. Total spend (USD) and where it went (input vs output vs cache read).
2. What fraction of tool calls were context-building (Read/Grep/Glob/Bash) vs code changes (Edit/Write)?
3. Did any episodes build intermediate helper scripts (intermediateArtifactCount > 0)? If yes, what were they for?
4. Which episodes had the largest bytesReadBeforeEdit — was it necessary?
5. One takeaway on how future work on this project could be more efficient.`,
        },
        {
          title: "Exploration heavy episodes",
          blurb: "Where did Claude thrash before writing code?",
          body:
`Use the claude-trace MCP server. Call list_episodes id="${ctx.sessionId}". Filter to episodes where toolCallCount >= 15 AND ended = true. For each, call get_episode with the episode_id to see the tool_calls sequence. Explain: what was the exploration pattern (grep-heavy, read-many-files, subagent-fan-out, bash-debug), and did the eventual Edit justify it?`,
        },
        {
          title: "Cost / time correlation",
          blurb: "Is spending more time actually delivering more code?",
          body:
`Use the claude-trace MCP server. Call list_episodes id="${ctx.sessionId}". For each episode compute (durationMs, costUsd, toolCallCount, bytesReadBeforeEdit, edited=(editFilePath != "")). Show the correlation coefficient between costUsd and each other metric. Flag episodes where costUsd > $2 but no edit happened (those are pure exploration cost).`,
        },
        {
          title: "Between prompts: how much processing?",
          blurb: "Time and code accessed between each of my prompts.",
          body:
`Use the claude-trace MCP server. Call list_episodes id="${ctx.sessionId}". For each episode, report: userPromptExcerpt (my prompt), durationMs, toolCallCount, bytesReadBeforeEdit (how much code you consumed before responding with code), costUsd, and whether it ended with an edit. Then compute means and medians across all episodes so I can see typical "prompt → code" cost/latency.`,
        },
        {
          title: "Intermediate tools built",
          blurb: "Detect helper scripts Claude wrote mid-session.",
          body:
`Use the claude-trace MCP server. Call list_episodes id="${ctx.sessionId}" and find every episode with intermediateArtifactCount > 0. For each, get_episode by id and inspect intermediateArtifactsJson. Group by directory (/tmp, /var/folders, etc). Was each helper script actually run afterward (look for Bash calls referencing the same path in the tool_calls list)? Flag helpers that were written but never executed.`,
        },
        {
          title: "What was Bash doing?",
          blurb: "Drill into the specific commands run.",
          body:
`Use the claude-trace MCP server. Call bash_breakdown scope="session" id="${ctx.sessionId}". Show the top 10 (program, action) pairs. For the top 3, call bash_samples scope="session" id="${ctx.sessionId}" program=X action=Y limit=20 and summarise what those commands were used for (build? test? git ops? debugging?). Flag any patterns with a high error rate.`,
        },
        {
          title: "Blast-radius of touched files",
          blurb: "Which files cascade the most changes?",
          body:
`Use the claude-trace MCP server. Call files_blast_radius scope="session" id="${ctx.sessionId}" limit=50. Sort by avgSiblingEdits desc — these are files whose edits usually pull other files along. For the top 5, hypothesise WHY (shared type? config? routing table? interface?) and suggest whether they'd benefit from splitting or better abstractions.`,
        },
      ];

    case "episode":
      return [
        {
          title: "Explain this episode",
          blurb: "Play-by-play of what Claude did for a single prompt.",
          body:
`Use the claude-trace MCP server. Call get_episode with episode_id=${ctx.episodeId}. Walk me through the toolCalls list in order. Explain: what was Claude looking for? Was any tool call redundant? Was the eventual Edit (${ctx.editFilePath || "no edit happened"}) an appropriate response to the original prompt: "${escape(ctx.userPrompt)}"?`,
        },
        {
          title: "Efficiency review",
          blurb: "Could this have been done with fewer tool calls?",
          body:
`Use the claude-trace MCP server. Call get_episode with episode_id=${ctx.episodeId}. The user prompt was: "${escape(ctx.userPrompt)}". Rate the exploration efficiency 1–5 and propose a shorter tool sequence that would have reached the same edit. Be specific — reference actual tool names and file paths from the toolCalls array.`,
        },
        {
          title: "Cost drivers",
          blurb: "What made this episode expensive (or cheap)?",
          body:
`Use the claude-trace MCP server. Call get_episode with episode_id=${ctx.episodeId}. Break down the costUsd: which of input / output / cache-read / cache-creation tokens dominated? What was the tokens-per-tool-call ratio? Compare to other episodes in the same session (call list_episodes id="${ctx.sessionId}" and compute quartiles).`,
        },
      ];
  }
}

function shortTitle(s: string): string {
  if (!s) return "session";
  if (s.length <= 40) return s;
  return s.slice(0, 40) + "…";
}

// escape stringifies safely so the prompt body can be pasted without JSON getting mangled.
function escape(s: string): string {
  return (s || "").replace(/\n/g, " ").replace(/"/g, '\\"').slice(0, 400);
}

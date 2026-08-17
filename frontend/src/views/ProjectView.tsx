import { useEffect, useMemo, useState } from "react";
import { ListSessions } from "../../wailsjs/go/main/App";
import type { store } from "../../wailsjs/go/models";
import { fmtBytes, fmtDate, fmtInt, fmtTokens, truncate } from "../format";
import { PromptsPanel } from "../components/PromptsPanel";

type Props = {
  slug: string;
  onOpen: (sessionId: string, projectSlug: string) => void;
  onBack: () => void;
};

export function ProjectView({ slug, onOpen, onBack }: Props) {
  const [sessions, setSessions] = useState<store.Session[]>([]);
  const [filter, setFilter] = useState("");
  const [cwd, setCwd] = useState("");
  const [displayName, setDisplayName] = useState(slug);

  useEffect(() => {
    ListSessions(slug).then((s) => {
      setSessions(s || []);
      if (s && s.length > 0) {
        setCwd(s[0].cwd || "");
      }
    });
  }, [slug]);
  useEffect(() => {
    const fromCwd = cwd.split("/").filter(Boolean).pop();
    if (fromCwd) setDisplayName(fromCwd);
  }, [cwd]);

  const filtered = useMemo(
    () => sessions.filter((s) => (s.aiTitle || "").toLowerCase().includes(filter.toLowerCase())),
    [sessions, filter],
  );

  const agg = useMemo(() => {
    const a = { toolCalls: 0, edits: 0, episodes: 0, input: 0, out: 0, cacheRead: 0, cost: 0 };
    for (const s of sessions) {
      a.toolCalls += s.toolCallCount;
      a.edits += s.editCount;
      a.episodes += s.episodeCount;
      a.input += s.totalInputTokens;
      a.out += s.totalOutputTokens;
      a.cacheRead += s.totalCacheReadTokens;
      a.cost += s.estimatedCostUsd || 0;
    }
    return a;
  }, [sessions]);

  return (
    <div className="project">
      <div className="crumbs">
        <a onClick={onBack}>← Projects</a>
        <span>/</span>
        <b>{slug}</b>
      </div>
      <div className="kpis">
        <Kpi label="Sessions" v={fmtInt(sessions.length)} />
        <Kpi label="Tool calls" v={fmtInt(agg.toolCalls)} />
        <Kpi label="Edits" v={fmtInt(agg.edits)} />
        <Kpi label="Episodes" v={fmtInt(agg.episodes)} />
        <Kpi label="Est. cost" v={`$${agg.cost.toFixed(2)}`} strong />
        <Kpi label="Input tok" v={fmtTokens(agg.input)} />
        <Kpi label="Cached read" v={fmtTokens(agg.cacheRead)} />
        <Kpi label="Output tok" v={fmtTokens(agg.out)} />
      </div>
      <div className="toolbar">
        <h2>Sessions ({sessions.length})</h2>
        <input
          className="filter"
          type="text"
          placeholder="Filter by AI title…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>
      <table className="tbl">
        <thead>
          <tr>
            <th>Title / session</th>
            <th>Started</th>
            <th className="num">Msgs</th>
            <th className="num">Tools</th>
            <th className="num">Edits</th>
            <th className="num">Episodes</th>
            <th className="num">Cost</th>
            <th className="num">Input</th>
            <th className="num">Cached read</th>
            <th className="num">Size</th>
          </tr>
        </thead>
        <tbody>
          {filtered.map((s) => (
            <tr key={s.sessionId} className="row" onClick={() => onOpen(s.sessionId, slug)}>
              <td>
                <div className="name">{truncate(s.aiTitle || "(untitled)", 80)}</div>
                <div className="sub mono">{s.sessionId} · {s.gitBranch || "no branch"}</div>
              </td>
              <td className="ts">{fmtDate(s.startedAt)}</td>
              <td className="num">{fmtInt(s.messageCount)}</td>
              <td className="num">{fmtInt(s.toolCallCount)}</td>
              <td className="num">{fmtInt(s.editCount)}</td>
              <td className="num">{fmtInt(s.episodeCount)}</td>
              <td className="num strong">{s.estimatedCostUsd > 0 ? `$${s.estimatedCostUsd.toFixed(2)}` : ""}</td>
              <td className="num">{fmtTokens(s.totalInputTokens)}</td>
              <td className="num">{fmtTokens(s.totalCacheReadTokens)}</td>
              <td className="num">{fmtBytes(s.sizeBytes)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <PromptsPanel ctx={{ kind: "project", slug, displayName, cwd }} />
    </div>
  );
}

function Kpi({ label, v, strong }: { label: string; v: string; strong?: boolean }) {
  return (
    <div className={"kpi" + (strong ? " strong" : "")}>
      <div className="kpi-value">{v}</div>
      <div className="kpi-label">{label}</div>
    </div>
  );
}

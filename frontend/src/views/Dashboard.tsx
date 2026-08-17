import { useEffect, useMemo, useState } from "react";
import { ListProjects } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { store } from "../../wailsjs/go/models";
import { fmtDate, fmtInt, fmtTokens } from "../format";
import { PromptsPanel } from "../components/PromptsPanel";

type Props = { onOpen: (slug: string) => void };

type SortKey = "displayName" | "sessionCount" | "totalToolCalls" | "totalEdits" | "lastSeenAt";

export function Dashboard({ onOpen }: Props) {
  const [projects, setProjects] = useState<store.Project[]>([]);
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({
    key: "lastSeenAt",
    dir: -1,
  });
  const [filter, setFilter] = useState("");

  async function refresh() {
    const ps = await ListProjects();
    setProjects(ps || []);
  }
  useEffect(() => {
    refresh();
    const off = EventsOn("ingest:done", () => refresh());
    return () => off();
  }, []);

  const sorted = useMemo(() => {
    const arr = projects.filter((p) =>
      p.displayName.toLowerCase().includes(filter.toLowerCase()),
    );
    arr.sort((a, b) => {
      const av = (a as any)[sort.key] ?? "";
      const bv = (b as any)[sort.key] ?? "";
      if (av === bv) return 0;
      return av > bv ? sort.dir : -sort.dir;
    });
    return arr;
  }, [projects, sort, filter]);

  function toggleSort(key: SortKey) {
    setSort((s) =>
      s.key === key ? { key, dir: (s.dir === 1 ? -1 : 1) as 1 | -1 } : { key, dir: -1 },
    );
  }

  return (
    <div className="dashboard">
      <div className="toolbar">
        <h2>Projects ({projects.length})</h2>
        <input
          className="filter"
          type="text"
          placeholder="Filter projects…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>
      <table className="tbl">
        <thead>
          <tr>
            <SortHeader label="Project" k="displayName" sort={sort} onClick={toggleSort} />
            <SortHeader label="Sessions" k="sessionCount" sort={sort} onClick={toggleSort} num />
            <SortHeader label="Tool calls" k="totalToolCalls" sort={sort} onClick={toggleSort} num />
            <SortHeader label="Edits" k="totalEdits" sort={sort} onClick={toggleSort} num />
            <SortHeader label="Est. cost" k="estimatedCostUsd" sort={sort} onClick={toggleSort} num />
            <th className="num">Input</th>
            <th className="num">Cached read</th>
            <SortHeader label="Last seen" k="lastSeenAt" sort={sort} onClick={toggleSort} />
          </tr>
        </thead>
        <tbody>
          {sorted.map((p) => (
            <tr key={p.slug} className="row" onClick={() => onOpen(p.slug)}>
              <td>
                <div className="name">{p.displayName || p.slug}</div>
                <div className="sub">{p.cwd || p.slug}</div>
              </td>
              <td className="num">{fmtInt(p.sessionCount)}</td>
              <td className="num">{fmtInt(p.totalToolCalls)}</td>
              <td className="num">{fmtInt(p.totalEdits)}</td>
              <td className="num strong">{p.estimatedCostUsd > 0 ? `$${p.estimatedCostUsd.toFixed(2)}` : ""}</td>
              <td className="num">{fmtTokens(p.totalInputTokens)}</td>
              <td className="num">{fmtTokens(p.totalCacheReadTokens)}</td>
              <td className="ts">{fmtDate(p.lastSeenAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {sorted.length === 0 && (
        <div className="empty">
          No projects. Click <b>Ingest</b> in the header to scan <code>~/.claude/projects</code>.
        </div>
      )}
      <PromptsPanel ctx={{ kind: "dashboard" }} />
    </div>
  );
}

function SortHeader<K extends string>({
  label, k, sort, onClick, num,
}: {
  label: string;
  k: K;
  sort: { key: string; dir: 1 | -1 };
  onClick: (k: any) => void;
  num?: boolean;
}) {
  const active = sort.key === k;
  return (
    <th className={num ? "num sortable" : "sortable"} onClick={() => onClick(k)}>
      {label}
      {active && (sort.dir === 1 ? " ▲" : " ▼")}
    </th>
  );
}

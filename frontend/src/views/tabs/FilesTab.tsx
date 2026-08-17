import { useEffect, useMemo, useState } from "react";
import { FilesBlastForSession } from "../../../wailsjs/go/main/App";
import type { store } from "../../../wailsjs/go/models";
import { fmtDate, fmtInt } from "../../format";

// FilesTab lists every file touched (read OR edited) in the session with
// blast-radius signals: distinct sessions/projects touching this file across
// ALL ingested history, and the average number of *other* files edited in the
// same episodes. A high avgSiblingEdits ⇒ this file tends to cascade.
export function FilesTab({ sessionId }: { sessionId: string }) {
  const [rows, setRows] = useState<store.FileBlastRadius[]>([]);
  const [sortKey, setSortKey] = useState<keyof store.FileBlastRadius>("editCount");
  const [dir, setDir] = useState<1 | -1>(-1);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    FilesBlastForSession(sessionId, 500).then((r) => setRows(r || []));
  }, [sessionId]);

  const sorted = useMemo(() => {
    const filt = rows.filter((r) => r.filePath.toLowerCase().includes(filter.toLowerCase()));
    filt.sort((a, b) => {
      const av = (a as any)[sortKey] ?? 0;
      const bv = (b as any)[sortKey] ?? 0;
      return av > bv ? dir : av < bv ? -dir : 0;
    });
    return filt;
  }, [rows, sortKey, dir, filter]);

  function toggle(k: keyof store.FileBlastRadius) {
    if (sortKey === k) setDir((d) => (d === 1 ? -1 : 1));
    else {
      setSortKey(k);
      setDir(-1);
    }
  }

  return (
    <>
      <p className="hint">
        Every file the session read or edited, with full paths.
        <b> Sessions/Projects</b> count how many *other* sessions and projects touched
        this same file (across all ingested history) — a proxy for how central
        the file is.
        <b> Avg sibling edits</b> = average number of other files edited in the
        same episodes as this one. High values mean editing this file tends to
        cascade into more changes.
      </p>
      <div className="toolbar">
        <input
          className="filter"
          type="text"
          placeholder="Filter by path substring…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <span className="hint">{sorted.length} of {rows.length}</span>
      </div>
      <table className="tbl">
        <thead>
          <tr>
            <th>File</th>
            <Th k="readCount" cur={sortKey} dir={dir} onClick={toggle} num>Reads</Th>
            <Th k="editCount" cur={sortKey} dir={dir} onClick={toggle} num>Edits</Th>
            <Th k="episodes" cur={sortKey} dir={dir} onClick={toggle} num>Episodes</Th>
            <Th k="sessions" cur={sortKey} dir={dir} onClick={toggle} num>Sessions*</Th>
            <Th k="projects" cur={sortKey} dir={dir} onClick={toggle} num>Projects*</Th>
            <Th k="avgSiblingEdits" cur={sortKey} dir={dir} onClick={toggle} num>Avg siblings</Th>
            <Th k="lastSeen" cur={sortKey} dir={dir} onClick={toggle}>Last touched</Th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => (
            <tr key={r.filePath}>
              <td className="mono">{r.filePath}</td>
              <td className="num">{fmtInt(r.readCount)}</td>
              <td className="num strong">{fmtInt(r.editCount)}</td>
              <td className="num">{fmtInt(r.episodes)}</td>
              <td className="num">{fmtInt(r.sessions)}</td>
              <td className="num">{fmtInt(r.projects)}</td>
              <td className="num">{r.avgSiblingEdits.toFixed(1)}</td>
              <td className="ts">{fmtDate(r.lastSeen)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="hint">* Across all ingested sessions/projects, not just this one.</p>
    </>
  );
}

function Th<K extends string>({
  k, cur, dir, onClick, num, children,
}: {
  k: K;
  cur: string;
  dir: 1 | -1;
  onClick: (k: any) => void;
  num?: boolean;
  children: any;
}) {
  return (
    <th className={(num ? "num " : "") + "sortable"} onClick={() => onClick(k)}>
      {children}
      {cur === k && (dir === 1 ? " ▲" : " ▼")}
    </th>
  );
}

import { useEffect, useMemo, useState } from "react";
import {
  ListEpisodes,
  ListMessages,
  CountMessages,
  ListWatermarks,
  AddWatermark,
  DeleteWatermark,
  SessionEpisodeStats,
  ToolBreakdownForSession,
  TopFilesForSession,
  EpisodeHistogramForSession,
  GetAppInfo,
  GetSession,
} from "../../wailsjs/go/main/App";
import type { main, store } from "../../wailsjs/go/models";
import { fmtBytes, fmtDate, fmtDuration, fmtInt, fmtTokens, truncate } from "../format";
import { PromptsPanel } from "../components/PromptsPanel";
import { BashTab } from "./tabs/BashTab";
import { FilesTab } from "./tabs/FilesTab";

type Props = {
  sessionId: string;
  onOpenEpisode: (episodeId: number) => void;
  onBack: () => void;
};

type Tab = "episodes" | "tools" | "bash" | "files" | "stats" | "timeline" | "watermarks";

export function SessionView({ sessionId, onOpenEpisode, onBack }: Props) {
  const [tab, setTab] = useState<Tab>("episodes");
  const [episodes, setEpisodes] = useState<store.Episode[]>([]);
  const [watermarks, setWatermarks] = useState<store.Watermark[]>([]);
  const [stats, setStats] = useState<main.EpisodeStatsBucket[]>([]);
  const [apiBase, setApiBase] = useState("");
  const [session, setSession] = useState<store.Session | null>(null);

  async function refresh() {
    const [e, w] = await Promise.all([ListEpisodes(sessionId), ListWatermarks(sessionId)]);
    setEpisodes(e || []);
    setWatermarks(w || []);
  }
  useEffect(() => {
    refresh();
    SessionEpisodeStats(sessionId).then((s) => setStats(s || []));
    GetAppInfo().then((i) => setApiBase(i.apiBase));
    GetSession(sessionId).then((s) => setSession(s || null));
  }, [sessionId]);

  return (
    <div className="session">
      <div className="crumbs">
        <a onClick={onBack}>← Back</a>
        <span>/</span>
        <b className="mono">{sessionId}</b>
        {apiBase && (
          <span className="dim">
            &nbsp;·&nbsp;<a href={`${apiBase}/export/episodes.csv?session=${sessionId}`}>episodes.csv</a>
            &nbsp;·&nbsp;<a href={`${apiBase}/export/tool-calls.csv?session=${sessionId}`}>tool-calls.csv</a>
          </span>
        )}
      </div>

      <div className="tabs">
        {(["episodes", "tools", "bash", "files", "stats", "timeline", "watermarks"] as Tab[]).map((t) => (
          <button
            key={t}
            className={"tab" + (tab === t ? " active" : "")}
            onClick={() => setTab(t)}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === "episodes" && (
        <EpisodesTab episodes={episodes} onOpen={(id) => onOpenEpisode(id)} />
      )}
      {tab === "tools" && <ToolsTab sessionId={sessionId} />}
      {tab === "bash" && <BashTab sessionId={sessionId} />}
      {tab === "files" && <FilesTab sessionId={sessionId} />}
      {tab === "stats" && <StatsTab stats={stats} />}
      {tab === "timeline" && (
        <TimelineTab
          sessionId={sessionId}
          watermarks={watermarks}
          onAddWatermark={async (m) => {
            const label = window.prompt(
              "Watermark label (e.g. 'about to refactor X'):",
              m.textContent.slice(0, 60),
            );
            if (!label) return;
            const note = window.prompt("Optional note:", "") || "";
            await AddWatermark(sessionId, m.uuid, label, note);
            refresh();
            SessionEpisodeStats(sessionId).then((s) => setStats(s || []));
          }}
        />
      )}
      {tab === "watermarks" && (
        <WatermarksTab
          watermarks={watermarks}
          onDelete={async (id) => {
            await DeleteWatermark(id);
            refresh();
            SessionEpisodeStats(sessionId).then((s) => setStats(s || []));
          }}
        />
      )}
      {session && (
        <PromptsPanel
          ctx={{
            kind: "session",
            sessionId,
            title: session.aiTitle || sessionId,
            projectSlug: session.projectSlug,
          }}
        />
      )}
    </div>
  );
}

// -----------------------------------------------------------------------------
// Tabs
// -----------------------------------------------------------------------------

function EpisodesTab({
  episodes,
  onOpen,
}: {
  episodes: store.Episode[];
  onOpen: (id: number) => void;
}) {
  const [sortKey, setSortKey] = useState<keyof store.Episode>("seq");
  const [dir, setDir] = useState<1 | -1>(1);
  const sorted = useMemo(() => {
    const a = [...episodes];
    a.sort((x, y) => {
      const xv = (x as any)[sortKey] ?? 0;
      const yv = (y as any)[sortKey] ?? 0;
      return xv > yv ? dir : xv < yv ? -dir : 0;
    });
    return a;
  }, [episodes, sortKey, dir]);

  function toggle(k: keyof store.Episode) {
    if (sortKey === k) setDir((d) => (d === 1 ? -1 : 1));
    else {
      setSortKey(k);
      setDir(-1);
    }
  }

  return (
    <>
      <p className="hint">
        An <b>episode</b> is one user turn → the next Edit/Write/NotebookEdit.
        Metrics show how much reading, searching, and tool activity happened
        before code was modified. Rows without an edit ran out of assistant
        activity before making a change.
      </p>
      <table className="tbl">
        <thead>
          <tr>
            <Th k="seq" cur={sortKey} dir={dir} onClick={toggle}>#</Th>
            <th>User prompt</th>
            <Th k="toolCallCount" cur={sortKey} dir={dir} onClick={toggle} num>
              Tools&nbsp;before&nbsp;edit
            </Th>
            <Th k="filesReadCount" cur={sortKey} dir={dir} onClick={toggle} num>Files</Th>
            <Th k="bashCount" cur={sortKey} dir={dir} onClick={toggle} num>Bash</Th>
            <Th k="searchCount" cur={sortKey} dir={dir} onClick={toggle} num>Search</Th>
            <Th k="subagentCount" cur={sortKey} dir={dir} onClick={toggle} num>Sub‑agent</Th>
            <Th k="bytesReadBeforeEdit" cur={sortKey} dir={dir} onClick={toggle} num>Bytes read</Th>
            <Th k="intermediateArtifactCount" cur={sortKey} dir={dir} onClick={toggle} num>Helpers</Th>
            <Th k="costUsd" cur={sortKey} dir={dir} onClick={toggle} num>Cost</Th>
            <Th k="cacheReadTokens" cur={sortKey} dir={dir} onClick={toggle} num>Cache read</Th>
            <Th k="durationMs" cur={sortKey} dir={dir} onClick={toggle} num>Duration</Th>
            <th>Edit</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((e) => (
            <tr
              key={e.id}
              className={"row" + (e.ended ? "" : " unended")}
              onClick={() => onOpen(e.id)}
            >
              <td className="num">{e.seq}</td>
              <td>{truncate(e.userPromptExcerpt || "", 100)}</td>
              <td className="num strong">{e.toolCallCount}</td>
              <td className="num">{e.filesReadCount}</td>
              <td className="num">{e.bashCount}</td>
              <td className="num">{e.searchCount}</td>
              <td className="num">{e.subagentCount}</td>
              <td className="num">{fmtBytes(e.bytesReadBeforeEdit)}</td>
              <td className="num">{e.intermediateArtifactCount || ""}</td>
              <td className="num strong">{e.costUsd > 0 ? `$${e.costUsd.toFixed(3)}` : ""}</td>
              <td className="num">{fmtTokens(e.cacheReadTokens)}</td>
              <td className="num">{fmtDuration(e.durationMs)}</td>
              <td className="mono">{truncate(e.editFilePath || (e.ended ? "" : "(no edit)"), 60)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

function ToolsTab({ sessionId }: { sessionId: string }) {
  const [tools, setTools] = useState<store.ToolBreakdown[]>([]);
  const [files, setFiles] = useState<store.FileTouchStat[]>([]);
  const [dist, setDist] = useState<store.EpisodeBucket[]>([]);
  useEffect(() => {
    ToolBreakdownForSession(sessionId).then((t) => setTools(t || []));
    TopFilesForSession(sessionId, 50).then((f) => setFiles(f || []));
    EpisodeHistogramForSession(sessionId).then((d) => setDist(d || []));
  }, [sessionId]);

  const maxToolCount = Math.max(1, ...tools.map((t) => t.count));
  const maxBucket = Math.max(1, ...dist.map((b) => b.count));

  return (
    <div className="tools-tab">
      <h3>Tool usage ({tools.length} distinct tools)</h3>
      <table className="tbl">
        <thead>
          <tr>
            <th>Tool</th>
            <th className="num">Calls</th>
            <th className="num">Edits</th>
            <th className="num">Errors</th>
            <th className="num">Result bytes</th>
            <th>Distribution</th>
          </tr>
        </thead>
        <tbody>
          {tools.map((t) => (
            <tr key={t.name}>
              <td className="strong">{t.name}</td>
              <td className="num">{fmtInt(t.count)}</td>
              <td className="num">{fmtInt(t.modifyingCount)}</td>
              <td className="num">{t.errorCount ? <span className="tag err">{t.errorCount}</span> : ""}</td>
              <td className="num">{fmtBytes(t.totalResultBytes)}</td>
              <td>
                <div className="bar" style={{ width: `${(t.count / maxToolCount) * 100}%` }} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h3 style={{ marginTop: 20 }}>Episode distribution — tools before edit</h3>
      <table className="tbl">
        <thead>
          <tr>
            <th>Bucket</th>
            <th className="num">Episodes</th>
            <th>Distribution</th>
          </tr>
        </thead>
        <tbody>
          {dist.map((b) => (
            <tr key={b.label}>
              <td className="strong">{b.label}</td>
              <td className="num">{b.count}</td>
              <td><div className="bar" style={{ width: `${(b.count / maxBucket) * 100}%` }} /></td>
            </tr>
          ))}
        </tbody>
      </table>

      <h3 style={{ marginTop: 20 }}>Files most touched ({files.length})</h3>
      <table className="tbl">
        <thead>
          <tr>
            <th>File</th>
            <th className="num">Reads</th>
            <th className="num">Edits</th>
            <th className="num">Total</th>
          </tr>
        </thead>
        <tbody>
          {files.map((f) => (
            <tr key={f.filePath}>
              <td className="mono">{f.filePath}</td>
              <td className="num">{fmtInt(f.readCount)}</td>
              <td className="num">{fmtInt(f.editCount)}</td>
              <td className="num strong">{fmtInt(f.totalCount)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function StatsTab({ stats }: { stats: main.EpisodeStatsBucket[] }) {
  if (stats.length === 0) return <div className="empty">No stats yet.</div>;
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>Bucket</th>
          <th className="num">Episodes</th>
          <th className="num">Ended</th>
          <th className="num">Avg tools before edit</th>
          <th className="num">Avg files</th>
          <th className="num">Avg input tok</th>
          <th className="num">Avg output tok</th>
          <th className="num">Avg duration</th>
          <th className="num">Bash</th>
          <th className="num">Search</th>
          <th className="num">Sub‑agent</th>
        </tr>
      </thead>
      <tbody>
        {stats.map((b, i) => (
          <tr key={i}>
            <td className="strong">{b.label}</td>
            <td className="num">{b.episodeCount}</td>
            <td className="num">{b.endedCount}</td>
            <td className="num">{b.avgToolCallsBeforeEdit.toFixed(1)}</td>
            <td className="num">{b.avgFilesReadBeforeEdit.toFixed(1)}</td>
            <td className="num">{fmtTokens(Math.round(b.avgInputTokens))}</td>
            <td className="num">{fmtTokens(Math.round(b.avgOutputTokens))}</td>
            <td className="num">{fmtDuration(Math.round(b.avgDurationMs))}</td>
            <td className="num">{fmtInt(b.totalBashCalls)}</td>
            <td className="num">{fmtInt(b.totalSearchCalls)}</td>
            <td className="num">{fmtInt(b.totalSubagentCalls)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function TimelineTab({
  sessionId,
  watermarks,
  onAddWatermark,
}: {
  sessionId: string;
  watermarks: store.Watermark[];
  onAddWatermark: (m: store.Message) => void;
}) {
  const [messages, setMessages] = useState<store.Message[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const pageSize = 300;

  useEffect(() => {
    CountMessages(sessionId).then(setTotal);
  }, [sessionId]);

  useEffect(() => {
    ListMessages(sessionId, pageSize, offset).then((m) => setMessages(m || []));
  }, [sessionId, offset]);

  const wmBySeq = useMemo(() => {
    const m = new Map<number, store.Watermark>();
    for (const w of watermarks) m.set(w.seq, w);
    return m;
  }, [watermarks]);

  const end = Math.min(offset + pageSize, total);

  return (
    <div className="timeline">
      <div className="page-bar">
        <button className="btn small" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - pageSize))}>← prev</button>
        <span className="hint">
          Messages {offset + 1}–{end} of {fmtInt(total)}
        </span>
        <button className="btn small" disabled={end >= total} onClick={() => setOffset(offset + pageSize)}>next →</button>
      </div>
      {messages.map((m) => (
        <div key={m.id}>
          {wmBySeq.get(m.seq) && (
            <div className="wm-line">▶ {wmBySeq.get(m.seq)!.label}</div>
          )}
          <div className={"tl-row role-" + m.role}>
            <div className="tl-meta">
              <span className="tl-seq">{m.seq}</span>
              <span className="tl-role">{m.role}{m.eventSubtype ? ":" + m.eventSubtype : ""}</span>
              <span className="tl-ts">{fmtDate(m.ts)}</span>
              {m.inputTokens > 0 && <span className="tl-tok">in {fmtTokens(m.inputTokens)}</span>}
              {m.outputTokens > 0 && <span className="tl-tok">out {fmtTokens(m.outputTokens)}</span>}
              {m.cacheReadTokens > 0 && (
                <span className="tl-tok">cache {fmtTokens(m.cacheReadTokens)}</span>
              )}
              {m.isSidechain && <span className="tl-tag">sidechain</span>}
              <button className="tl-wm-btn" onClick={() => onAddWatermark(m)}>+ mark</button>
            </div>
            <div className="tl-text">{truncate(m.textContent || "", 400) || <i>(no text)</i>}</div>
          </div>
        </div>
      ))}
    </div>
  );
}

function WatermarksTab({
  watermarks,
  onDelete,
}: {
  watermarks: store.Watermark[];
  onDelete: (id: number) => void;
}) {
  if (watermarks.length === 0) {
    return (
      <div className="empty">
        No watermarks. Open the <b>timeline</b> tab and click <b>+ mark</b> beside a message to add one.
      </div>
    );
  }
  return (
    <table className="tbl">
      <thead>
        <tr>
          <th>Seq</th>
          <th>Label</th>
          <th>Note</th>
          <th>Anchor message</th>
          <th>Created</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {watermarks.map((w) => (
          <tr key={w.id}>
            <td className="num">{w.seq || ""}</td>
            <td className="strong">{w.label}</td>
            <td>{w.note}</td>
            <td className="mono">{truncate(w.messageUuid, 20)}</td>
            <td className="ts">{fmtDate(w.createdAt)}</td>
            <td><button className="btn small" onClick={() => onDelete(w.id)}>delete</button></td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Th({
  k, cur, dir, onClick, num, children,
}: {
  k: keyof store.Episode;
  cur: keyof store.Episode;
  dir: 1 | -1;
  onClick: (k: keyof store.Episode) => void;
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

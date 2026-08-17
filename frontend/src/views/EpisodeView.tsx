import { useEffect, useState } from "react";
import {
  ListEpisodes,
  ListToolCallsForEpisode,
  GetMessageRaw,
} from "../../wailsjs/go/main/App";
import type { store } from "../../wailsjs/go/models";
import { fmtBytes, fmtDuration, fmtTokens, truncate } from "../format";
import { PromptsPanel } from "../components/PromptsPanel";
import { GetSession } from "../../wailsjs/go/main/App";

type Props = {
  episodeId: number;
  sessionId: string;
  onBack: () => void;
};

export function EpisodeView({ episodeId, sessionId, onBack }: Props) {
  const [ep, setEp] = useState<store.Episode | null>(null);
  const [tcs, setTcs] = useState<store.ToolCall[]>([]);
  const [session, setSession] = useState<store.Session | null>(null);
  const [rawOpen, setRawOpen] = useState<{ id: number; body: string } | null>(null);

  useEffect(() => {
    ListEpisodes(sessionId).then((es) => {
      setEp((es || []).find((e) => e.id === episodeId) || null);
    });
    ListToolCallsForEpisode(episodeId).then((t) => setTcs(t || []));
    GetSession(sessionId).then((s) => setSession(s || null));
  }, [episodeId, sessionId]);

  async function openRaw(id: number) {
    const body = await GetMessageRaw(id);
    setRawOpen({ id, body });
  }

  const files = ep?.filesReadJson ? (JSON.parse(ep.filesReadJson) as string[]) : [];

  return (
    <div className="episode">
      <div className="crumbs">
        <a onClick={onBack}>← Back to session</a>
      </div>
      {ep && (
        <>
          <h2>Episode {ep.seq}</h2>
          <blockquote className="user-prompt">{ep.userPromptExcerpt}</blockquote>
          <div className="kpis">
            <Kpi label="Tools before edit" v={String(ep.toolCallCount)} strong />
            <Kpi label="Bytes read" v={fmtBytes(ep.bytesReadBeforeEdit)} />
            <Kpi label="Helpers built" v={String(ep.intermediateArtifactCount)} />
            <Kpi label="Est. cost" v={ep.costUsd > 0 ? `$${ep.costUsd.toFixed(3)}` : "—"} strong />
            <Kpi label="Duration" v={fmtDuration(ep.durationMs)} />
            <Kpi label="Files read" v={String(ep.filesReadCount)} />
            <Kpi label="Bash" v={String(ep.bashCount)} />
            <Kpi label="Search" v={String(ep.searchCount)} />
            <Kpi label="Sub‑agent" v={String(ep.subagentCount)} />
            <Kpi label="Input tok" v={fmtTokens(ep.inputTokens)} />
            <Kpi label="Output tok" v={fmtTokens(ep.outputTokens)} />
            <Kpi label="Cache read" v={fmtTokens(ep.cacheReadTokens)} />
          </div>
          {ep.editFilePath && (
            <div className="edit-target">
              Edit target: <code>{ep.editFilePath}</code>
            </div>
          )}
          {!ep.ended && <div className="unended-note">This episode ended without an edit.</div>}

          {files.length > 0 && (
            <>
              <h3>Files touched ({files.length})</h3>
              <ul className="files">
                {files.map((f) => (
                  <li key={f} className="mono">{f}</li>
                ))}
              </ul>
            </>
          )}
          {ep.intermediateArtifactCount > 0 && (
            <>
              <h3>Intermediate helpers built ({ep.intermediateArtifactCount})</h3>
              <ul className="files">
                {(JSON.parse(ep.intermediateArtifactsJson) as string[]).map((f) => (
                  <li key={f} className="mono">{f}</li>
                ))}
              </ul>
            </>
          )}
        </>
      )}

      <h3>Tool call sequence ({tcs.length})</h3>
      <table className="tbl">
        <thead>
          <tr>
            <th>#</th>
            <th>Tool</th>
            <th>Target</th>
            <th className="num">Result bytes</th>
            <th>Flags</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {tcs.map((tc, i) => (
            <tr key={tc.id}>
              <td className="num">{i + 1}</td>
              <td className="strong">{tc.name}</td>
              <td className="mono">{truncate(tc.filePath || parseFirstArg(tc.inputJson), 100)}</td>
              <td className="num">{fmtTokens(tc.resultSize)}</td>
              <td>
                {tc.isModifying && <span className="tag mod">edit</span>}
                {tc.isRead && <span className="tag read">read</span>}
                {tc.isBash && <span className="tag bash">bash</span>}
                {tc.isSearch && <span className="tag search">search</span>}
                {tc.isSubagent && <span className="tag sub">sub</span>}
                {tc.resultIsError && <span className="tag err">error</span>}
              </td>
              <td>
                <button className="btn small" onClick={() => openRaw(tc.messageId)}>raw</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {rawOpen && (
        <div className="modal" onClick={() => setRawOpen(null)}>
          <div className="modal-body" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <span>Raw message #{rawOpen.id}</span>
              <button className="btn small" onClick={() => setRawOpen(null)}>close</button>
            </div>
            <pre>{tryPretty(rawOpen.body)}</pre>
          </div>
        </div>
      )}

      {ep && session && (
        <PromptsPanel
          ctx={{
            kind: "episode",
            episodeId: ep.id,
            sessionId,
            projectSlug: session.projectSlug,
            editFilePath: ep.editFilePath,
            userPrompt: ep.userPromptExcerpt,
          }}
        />
      )}
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

function parseFirstArg(json: string): string {
  if (!json) return "";
  try {
    const o = JSON.parse(json);
    if (o.command) return String(o.command);
    if (o.pattern) return String(o.pattern);
    if (o.query) return String(o.query);
    if (o.description) return String(o.description);
    return "";
  } catch {
    return "";
  }
}

function tryPretty(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

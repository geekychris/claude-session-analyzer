import { useEffect, useState } from "react";
import {
  BashBreakdownForSession,
  BashSamplesForSession,
} from "../../../wailsjs/go/main/App";
import type { store } from "../../../wailsjs/go/models";
import { fmtDate, fmtInt, fmtBytes } from "../../format";

// BashTab shows what shelled out in a session: grouped by (program, action),
// with drill-in to the actual commands. This is the "what was Claude really
// doing inside Bash?" view.
export function BashTab({ sessionId }: { sessionId: string }) {
  const [rows, setRows] = useState<store.BashBreakdownRow[]>([]);
  const [drill, setDrill] = useState<{ program: string; action: string } | null>(null);

  useEffect(() => {
    BashBreakdownForSession(sessionId).then((r) => setRows(r || []));
  }, [sessionId]);

  const max = Math.max(1, ...rows.map((r) => r.count));

  return (
    <>
      <p className="hint">
        Bash calls broken down by program and subcommand — click a row to see
        the actual commands and their descriptions. Programs that don't have a
        subcommand column (grep, curl, cat…) list one row per program.
      </p>
      <table className="tbl">
        <thead>
          <tr>
            <th>Program</th>
            <th>Action</th>
            <th className="num">Count</th>
            <th className="num">Errors</th>
            <th>Distribution</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.program + "|" + r.action}>
              <td className="strong">{r.program}</td>
              <td>{r.action || <span className="dim">—</span>}</td>
              <td className="num">{fmtInt(r.count)}</td>
              <td className="num">{r.errorCount ? <span className="tag err">{r.errorCount}</span> : ""}</td>
              <td>
                <div className="bar" style={{ width: `${(r.count / max) * 100}%` }} />
              </td>
              <td>
                <button className="btn small" onClick={() => setDrill({ program: r.program, action: r.action })}>
                  commands →
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {drill && (
        <BashSamplesModal
          sessionId={sessionId}
          program={drill.program}
          action={drill.action}
          onClose={() => setDrill(null)}
        />
      )}
    </>
  );
}

function BashSamplesModal({
  sessionId, program, action, onClose,
}: {
  sessionId: string;
  program: string;
  action: string;
  onClose: () => void;
}) {
  const [samples, setSamples] = useState<store.BashCommandSample[]>([]);
  useEffect(() => {
    BashSamplesForSession(sessionId, program, action, 200).then((s) => setSamples(s || []));
  }, [sessionId, program, action]);

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body large" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span>
            <b>{program}</b>{action ? ` ${action}` : ""} — {samples.length} commands
          </span>
          <button className="btn small" onClick={onClose}>close</button>
        </div>
        <div className="samples-list">
          {samples.map((s) => (
            <div key={s.id} className={"sample" + (s.isError ? " err" : "")}>
              <div className="sample-meta">
                <span className="mono">#{s.seq}</span>
                <span className="ts">{fmtDate(s.ts)}</span>
                {s.description && <span className="sample-desc">{s.description}</span>}
                <span className="sample-bytes">{fmtBytes(s.resultBytes)}</span>
                {s.isError && <span className="tag err">error</span>}
              </div>
              <pre className="sample-cmd">{s.command}</pre>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

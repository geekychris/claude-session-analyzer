import { useEffect, useState } from "react";
import { GetAppInfo, StartIngest } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { fmtInt } from "../format";

type Props = {
  onHome: () => void;
  onSearch: (q: string) => void;
};

export function HeaderBar({ onHome, onSearch }: Props) {
  const [ingesting, setIngesting] = useState(false);
  const [progress, setProgress] = useState<{ path: string; total: number } | null>(null);
  const [query, setQuery] = useState("");
  const [dbPath, setDBPath] = useState("");

  useEffect(() => {
    GetAppInfo().then((i) => {
      setDBPath(i.dbPath);
      setIngesting(i.ingesting);
    });
    const off1 = EventsOn("ingest:progress", (p: any) => {
      setProgress({ path: p.path, total: p.total });
    });
    const off2 = EventsOn("ingest:done", (_p: any) => {
      setIngesting(false);
      setProgress(null);
    });
    return () => {
      off1();
      off2();
    };
  }, []);

  async function runIngest() {
    if (ingesting) return;
    setIngesting(true);
    setProgress(null);
    try {
      await StartIngest();
    } catch (e) {
      console.error(e);
      setIngesting(false);
    }
  }

  return (
    <header className="header">
      <button className="brand" onClick={onHome}>claude‑trace</button>
      <form
        className="search"
        onSubmit={(e) => {
          e.preventDefault();
          if (query.trim()) onSearch(query.trim());
        }}
      >
        <input
          type="text"
          placeholder="Search messages (FTS5 syntax: word AND phrase*)"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </form>
      <div className="header-right">
        {ingesting && progress && (
          <span className="ingest-status">
            ingesting: {shortPath(progress.path)} · {fmtInt(progress.total)} msgs
          </span>
        )}
        {ingesting && !progress && <span className="ingest-status">scanning…</span>}
        <button className="btn" onClick={runIngest} disabled={ingesting}>
          {ingesting ? "…" : "Ingest"}
        </button>
        <span className="db-path" title={dbPath}>db</span>
      </div>
    </header>
  );
}

function shortPath(p: string): string {
  const parts = p.split("/");
  return parts[parts.length - 1] ?? p;
}

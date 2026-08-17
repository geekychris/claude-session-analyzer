import { useEffect, useState } from "react";
import { Search } from "../../wailsjs/go/main/App";
import type { store } from "../../wailsjs/go/models";
import { fmtDate } from "../format";

type Props = { query: string; onBack: () => void };

export function SearchView({ query, onBack }: Props) {
  const [hits, setHits] = useState<store.SearchHit[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    setError(null);
    Search(query, 200)
      .then((h) => setHits(h || []))
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  }, [query]);

  return (
    <div className="search-view">
      <div className="crumbs">
        <a onClick={onBack}>← Back</a>
        <span>/</span>
        <b>search: {query}</b>
      </div>
      {loading && <div className="empty">searching…</div>}
      {error && <div className="empty error">{error}</div>}
      {!loading && !error && hits.length === 0 && <div className="empty">No hits.</div>}
      <ul className="hits">
        {hits.map((h) => (
          <li key={h.messageId}>
            <div className="hit-meta">
              <span className="tag">{h.role}</span>
              <span className="mono">{h.sessionId.slice(0, 8)}</span>
              <span className="ts">{fmtDate(h.ts)}</span>
              <span className="dim">{h.projectSlug}</span>
            </div>
            <div className="hit-snip" dangerouslySetInnerHTML={{ __html: h.snippet }} />
          </li>
        ))}
      </ul>
    </div>
  );
}

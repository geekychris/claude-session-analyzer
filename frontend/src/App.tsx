import { useEffect, useState } from "react";
import "./App.css";
import { Dashboard } from "./views/Dashboard";
import { ProjectView } from "./views/ProjectView";
import { SessionView } from "./views/SessionView";
import { EpisodeView } from "./views/EpisodeView";
import { SearchView } from "./views/SearchView";
import { HeaderBar } from "./components/HeaderBar";
import type { Nav } from "./nav";

export default function App() {
  const [nav, setNav] = useState<Nav>({ view: "dashboard" });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") back();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nav]);

  function back() {
    setNav((n) => {
      if (n.view === "episode")
        return { view: "session", sessionId: n.sessionId, projectSlug: n.projectSlug };
      if (n.view === "session") return { view: "project", slug: n.projectSlug };
      if (n.view === "project" || n.view === "search") return { view: "dashboard" };
      return n;
    });
  }

  return (
    <div className="app">
      <HeaderBar
        onHome={() => setNav({ view: "dashboard" })}
        onSearch={(q) => setNav({ view: "search", query: q })}
      />
      <main className="content">
        {nav.view === "dashboard" && (
          <Dashboard onOpen={(slug) => setNav({ view: "project", slug })} />
        )}
        {nav.view === "project" && (
          <ProjectView
            slug={nav.slug}
            onOpen={(sessionId, projectSlug) =>
              setNav({ view: "session", sessionId, projectSlug })
            }
            onBack={back}
          />
        )}
        {nav.view === "session" && (
          <SessionView
            sessionId={nav.sessionId}
            onOpenEpisode={(episodeId) =>
              setNav({
                view: "episode",
                episodeId,
                sessionId: nav.sessionId,
                projectSlug: nav.projectSlug,
              })
            }
            onBack={back}
          />
        )}
        {nav.view === "episode" && (
          <EpisodeView episodeId={nav.episodeId} sessionId={nav.sessionId} onBack={back} />
        )}
        {nav.view === "search" && <SearchView query={nav.query} onBack={back} />}
      </main>
    </div>
  );
}

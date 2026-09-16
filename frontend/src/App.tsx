import { useEffect, useState } from "react";
import "./App.css";
import { Dashboard } from "./views/Dashboard";
import { ProjectView } from "./views/ProjectView";
import { SessionView } from "./views/SessionView";
import { EpisodeView } from "./views/EpisodeView";
import { SearchView } from "./views/SearchView";
import { HeaderBar } from "./components/HeaderBar";
import { GetInitialProject } from "../wailsjs/go/main/App";
import { EventsOn, EventsOff } from "../wailsjs/runtime/runtime";
import type { Nav } from "./nav";

export default function App() {
  const [nav, setNav] = useState<Nav>({ view: "dashboard" });

  // Deep-link: if the app was launched with --project <slug>, jump straight
  // to that project on mount. Chief uses this to open the analyzer directly
  // to the project currently focused in its sidebar.
  useEffect(() => {
    let cancelled = false;
    GetInitialProject()
      .then((slug) => {
        if (!cancelled && typeof slug === "string" && slug.length > 0) {
          setNav({ view: "project", slug });
        }
      })
      .catch(() => {
        /* older backend without this binding — stay on dashboard */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Redirect-in-place: when an external client POSTs to /api/v1/navigate,
  // the backend emits a Wails "navigate" event. We validate the payload
  // (which mirrors the Nav discriminated union) and update state, so the
  // window redraws without needing a second app instance.
  useEffect(() => {
    const handler = (payload: unknown) => {
      const nav = coerceNav(payload);
      if (nav) setNav(nav);
    };
    EventsOn("navigate", handler);
    return () => EventsOff("navigate");
  }, []);

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

// coerceNav validates and narrows an unknown payload (received via the Wails
// "navigate" runtime event, originating from POST /api/v1/navigate) into a
// well-formed Nav. Returns null if invalid — the SPA then stays where it is
// rather than crashing on a malformed external call.
function coerceNav(payload: unknown): Nav | null {
  if (!payload || typeof payload !== "object") return null;
  const p = payload as Record<string, unknown>;
  const view = typeof p.view === "string" ? p.view : "";
  switch (view) {
    case "dashboard":
      return { view: "dashboard" };
    case "project":
      if (typeof p.slug === "string" && p.slug.length > 0) {
        return { view: "project", slug: p.slug };
      }
      return null;
    case "session":
      if (
        typeof p.sessionId === "string" && p.sessionId.length > 0 &&
        typeof p.projectSlug === "string" && p.projectSlug.length > 0
      ) {
        return { view: "session", sessionId: p.sessionId, projectSlug: p.projectSlug };
      }
      return null;
    case "episode":
      if (
        typeof p.episodeId === "number" &&
        typeof p.sessionId === "string" && p.sessionId.length > 0 &&
        typeof p.projectSlug === "string" && p.projectSlug.length > 0
      ) {
        return {
          view: "episode",
          episodeId: p.episodeId,
          sessionId: p.sessionId,
          projectSlug: p.projectSlug,
        };
      }
      return null;
    case "search":
      if (typeof p.query === "string") {
        return { view: "search", query: p.query };
      }
      return null;
    default:
      return null;
  }
}

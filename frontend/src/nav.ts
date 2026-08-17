// Route/view state for the SPA. Wails apps don't need a real router — a
// discriminated union keeps navigation type-safe and the components stateless.

export type Nav =
  | { view: "dashboard" }
  | { view: "project"; slug: string }
  | { view: "session"; sessionId: string; projectSlug: string }
  | { view: "episode"; episodeId: number; sessionId: string; projectSlug: string }
  | { view: "search"; query: string };

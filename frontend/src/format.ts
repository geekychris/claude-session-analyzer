// Small formatting helpers used across the views.

export function fmtInt(n: number | undefined | null): string {
  if (n == null) return "";
  return n.toLocaleString();
}

export function fmtTokens(n: number | undefined | null): string {
  if (n == null) return "";
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "k";
  return String(n);
}

export function fmtDuration(ms: number | undefined | null): string {
  if (!ms) return "";
  const s = ms / 1000;
  if (s < 60) return s.toFixed(1) + "s";
  const m = s / 60;
  if (m < 60) return m.toFixed(1) + "m";
  return (m / 60).toFixed(1) + "h";
}

export function fmtDate(ts: string | undefined | null): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  return d.toLocaleString(undefined, {
    year: "numeric", month: "short", day: "2-digit",
    hour: "2-digit", minute: "2-digit",
  });
}

export function fmtBytes(n: number | undefined | null): string {
  if (!n) return "0";
  if (n >= 1024 * 1024 * 1024) return (n / 1073741824).toFixed(1) + " GiB";
  if (n >= 1024 * 1024) return (n / 1048576).toFixed(1) + " MiB";
  if (n >= 1024) return (n / 1024).toFixed(1) + " KiB";
  return n + " B";
}

export function truncate(s: string, n: number): string {
  if (!s) return "";
  if (s.length <= n) return s;
  return s.slice(0, n) + "…";
}

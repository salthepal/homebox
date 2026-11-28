// Pure helpers shared by the Projects UI (pages, cards, and the P4 item tab).
import type { XProject, XProjectStatus, XProjectStep, XStepStatus } from "./api/client";

export const PROJECT_STATUSES: XProjectStatus[] = ["idea", "planning", "in_progress", "on_hold", "done", "cancelled"];
export const STEP_STATUSES: XStepStatus[] = ["todo", "doing", "blocked", "done", "skipped"];

/** "2027-03-14" or "2027-03-14T00:00:00Z" -> "2027-03-14" (for <input type=date>); null -> "". */
export function toDateInput(v: string | null | undefined): string {
  if (!v || v.startsWith("0001-01-01")) return "";
  return v.slice(0, 10);
}

/** "" -> null, otherwise the date string. */
export function fromDateInput(v: string): string | null {
  return v ? v : null;
}

/** "" or non-numeric/negative -> null. */
export function parseMoney(v: string | number | null | undefined): number | null {
  if (v === null || v === undefined || v === "") return null;
  const n = typeof v === "number" ? v : Number(v);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

/** Plain currency formatting; pages use `useMoney()` so the group's configured currency applies. */
export function formatMoney(n: number | null | undefined, currency = "USD"): string {
  const code = /^[A-Za-z]{3}$/.test(currency) ? currency.toUpperCase() : "USD";
  return new Intl.NumberFormat(undefined, { style: "currency", currency: code }).format(n ?? 0);
}

/**
 * Returns the URL only when it is http(s); anything else (javascript:, data:, ...) is
 * rejected so a user-supplied step link can never become an active href.
 */
export function safeHttpUrl(v: string | null | undefined): string | null {
  if (!v) return null;
  try {
    const u = new URL(v.trim());
    return u.protocol === "http:" || u.protocol === "https:" ? u.toString() : null;
  } catch {
    return null;
  }
}

export function formatDate(v: string | null | undefined): string {
  const d = toDateInput(v);
  if (!d) return "";
  const [y, m, day] = d.split("-").map(Number);
  return new Date(y!, m! - 1, day).toLocaleDateString();
}

/** Formats an RFC3339 instant (started_at, completed_at) in the viewer's timezone. */
export function formatTimestamp(v: string | null | undefined): string {
  if (!v || v.startsWith("0001-01-01")) return "";
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString();
}

export function isStepFinished(s: XProjectStep): boolean {
  return s.status === "done" || s.status === "skipped";
}

export function projectProgress(p: Pick<XProject, "steps">): { done: number; total: number; percent: number } {
  const total = p.steps.length;
  const done = p.steps.filter(isStepFinished).length;
  return { done, total, percent: total === 0 ? 0 : Math.round((done / total) * 100) };
}

export function projectCosts(p: Pick<XProject, "steps" | "budget">): {
  estimated: number;
  actual: number;
  budget: number | null;
  overBudget: boolean;
} {
  const estimated = p.steps.reduce((sum, s) => sum + (s.est_cost ?? 0), 0);
  const actual = p.steps.reduce((sum, s) => sum + (s.actual_cost ?? 0), 0);
  return { estimated, actual, budget: p.budget, overBudget: p.budget !== null && actual > p.budget };
}

/** True when every step is done or skipped (and there is at least one) but the project is still open. */
export function shouldPromptDone(p: Pick<XProject, "steps" | "status">): boolean {
  return p.steps.length > 0 && p.steps.every(isStepFinished) && p.status !== "done" && p.status !== "cancelled";
}

export function groupByStatus(projects: XProject[]): Record<XProjectStatus, XProject[]> {
  const out = Object.fromEntries(PROJECT_STATUSES.map(s => [s, [] as XProject[]])) as Record<
    XProjectStatus,
    XProject[]
  >;
  for (const p of projects) out[p.status]?.push(p);
  for (const s of PROJECT_STATUSES) out[s].sort((a, b) => b.priority - a.priority || a.name.localeCompare(b.name));
  return out;
}

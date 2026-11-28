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

export function formatMoney(n: number | null | undefined): string {
  return new Intl.NumberFormat(undefined, { style: "currency", currency: "USD" }).format(n ?? 0);
}

export function formatDate(v: string | null | undefined): string {
  const d = toDateInput(v);
  if (!d) return "";
  const [y, m, day] = d.split("-").map(Number);
  return new Date(y!, m! - 1, day).toLocaleDateString();
}

export function isStepFinished(s: XProjectStep): boolean {
  return s.status === "done" || s.status === "skipped";
}

export function projectProgress(p: Pick<XProject, "steps">): { done: number; total: number; percent: number } {
  const total = p.steps.length;
  const done = p.steps.filter(s => s.status === "done").length;
  return { done, total, percent: total === 0 ? 0 : Math.round((done / total) * 100) };
}

export function projectCosts(p: Pick<XProject, "steps" | "budget">): {
  estimated: number;
  actual: number;
  budget: number | null;
  overBudget: boolean;
} {
  const estimated = p.steps.reduce((sum, s) => sum + (s.estCost ?? 0), 0);
  const actual = p.steps.reduce((sum, s) => sum + (s.actualCost ?? 0), 0);
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

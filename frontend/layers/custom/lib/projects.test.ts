import { describe, expect, test } from "vitest";
import type { XProject } from "./api/client";
import { formatMoney, formatTimestamp, projectProgress, safeHttpUrl, shouldPromptDone } from "./projects";

type Steps = Pick<XProject, "steps">["steps"];
const steps = (...status: string[]) => status.map(s => ({ status: s })) as unknown as Steps;

describe("safeHttpUrl", () => {
  test("allows http and https", () => {
    expect(safeHttpUrl("https://example.com/a?b=1")).toBe("https://example.com/a?b=1");
    expect(safeHttpUrl("http://example.com")).toBe("http://example.com/");
  });

  test.each([
    "javascript:alert(1)",
    "JaVaScRiPt:alert(1)",
    "data:text/html,<script>1</script>",
    "vbscript:x",
    "//evil.example",
    "not a url",
    "",
    "  ",
  ])("rejects %j", v => {
    expect(safeHttpUrl(v)).toBeNull();
  });

  test("null and undefined", () => {
    expect(safeHttpUrl(null)).toBeNull();
    expect(safeHttpUrl(undefined)).toBeNull();
  });
});

describe("projectProgress", () => {
  test("counts skipped steps as finished, like shouldPromptDone", () => {
    const p = { steps: steps("done", "skipped") };
    expect(projectProgress(p)).toEqual({ done: 2, total: 2, percent: 100 });
    expect(shouldPromptDone({ ...p, status: "in_progress" })).toBe(true);
  });

  test("open steps are not counted", () => {
    expect(projectProgress({ steps: steps("done", "todo", "doing", "blocked") })).toEqual({
      done: 1,
      total: 4,
      percent: 25,
    });
  });

  test("empty project", () => {
    expect(projectProgress({ steps: [] })).toEqual({
      done: 0,
      total: 0,
      percent: 0,
    });
  });
});

describe("formatMoney", () => {
  test("uses the given currency and falls back to USD for junk", () => {
    expect(formatMoney(12.5, "EUR")).toContain("12.50");
    expect(formatMoney(12.5, "EUR")).toContain("€");
    expect(formatMoney(12.5, "nope")).toContain("$");
    expect(formatMoney(null)).toContain("0.00");
  });
});

describe("formatTimestamp", () => {
  test("parses RFC3339 instants instead of truncating to the UTC date", () => {
    const iso = "2026-11-28T00:30:00Z";
    expect(formatTimestamp(iso)).toBe(new Date(iso).toLocaleDateString());
  });

  test("empty, zero and invalid values", () => {
    expect(formatTimestamp(null)).toBe("");
    expect(formatTimestamp("0001-01-01T00:00:00Z")).toBe("");
    expect(formatTimestamp("garbage")).toBe("");
  });
});

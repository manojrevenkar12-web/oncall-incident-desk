import type { Stat } from "./report";

export const pct = (x: number) => `${(100 * x).toFixed(1)}%`;
export const num = (x: number, digits = 1) => x.toFixed(digits);
export const range = (s: Stat, f: (x: number) => string) => (s.lo === s.hi ? "" : `${f(s.lo)}–${f(s.hi)}`);

const days = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export function when(iso: string) {
  const d = new Date(iso);
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  return `${days[d.getUTCDay()]} ${hh}:${mm} UTC`;
}

export function minutesBetween(a: string, b: string) {
  return Math.round((new Date(b).getTime() - new Date(a).getTime()) / 60000);
}

export const KIND_NAMES: Record<string, string> = {
  full: "went down",
  partial: "lost one endpoint",
  regional: "failed in one region",
  degraded: "turned intermittent",
};

export const ERROR_NAMES: Record<string, string> = {
  internal_detail: "Leaked an internal detail",
  recovery_promise: "Promised a recovery time",
  unaffected_component: "Named a working component",
  unconfirmed_cause: "Stated an unconfirmed cause",
  missing_component: "Named no affected component",
  shape: "Empty or too long",
};

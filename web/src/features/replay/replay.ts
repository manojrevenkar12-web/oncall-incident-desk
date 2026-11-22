import type { Lane, LaneEvent, Showcase } from "../../shared/report";

export type Group = { key: string; kind: LaneEvent["kind"]; minute: number; events: LaneEvent[]; about: boolean };

// Events of one kind in the same minute become one marker with a count, so twelve pages read as twelve.
export function groups(lane: Lane): Group[] {
  const out = new Map<string, Group>();
  for (const e of lane.events ?? []) {
    const minute = Math.round(e.offset);
    const key = `${e.kind}@${minute}`;
    const g = out.get(key) ?? { key, kind: e.kind, minute, events: [], about: false };
    g.events.push(e);
    g.about = g.about || e.about;
    out.set(key, g);
  }
  return [...out.values()].sort((a, b) => a.minute - b.minute || order(a.kind) - order(b.kind));
}

const order = (k: LaneEvent["kind"]) => ({ page: 0, ack: 1, refused: 2, post: 3 })[k];

export type Summary = { pages: number; people: number; firstAck: number | null; firstPost: number | null; badPosts: number; refused: number };

export function summary(lane: Lane): Summary {
  const ev = lane.events ?? [];
  const pages = ev.filter((e) => e.kind === "page" && e.about);
  const acks = ev.filter((e) => e.kind === "ack" && e.about);
  const posts = ev.filter((e) => e.kind === "post" && e.about && e.level !== "resolved");
  return {
    pages: pages.length,
    people: new Set(pages.map((p) => p.who)).size,
    firstAck: acks.length ? Math.min(...acks.map((a) => a.offset)) : null,
    firstPost: posts.length ? Math.min(...posts.map((p) => p.offset)) : null,
    badPosts: ev.filter((e) => e.kind === "post" && e.bad).length,
    refused: ev.filter((e) => e.kind === "refused").length,
  };
}

export function span(sc: Showcase) {
  const offs = sc.series.map((p) => p.offset);
  return { min: Math.min(...offs, -5), max: Math.max(...offs, 30) };
}

export function locations(sc: Showcase): string[] {
  const set = new Set<string>();
  for (const p of sc.series) for (const l of Object.keys(p.failing)) set.add(l);
  return [...set].sort();
}

// The share of the affected checks failing at one location in one minute.
export function failingShare(sc: Showcase, loc: string, offset: number, nLocations: number): number {
  const p = sc.series.find((s) => s.offset === offset);
  if (!p || p.runs === 0) return 0;
  return (p.failing[loc] ?? 0) / (p.runs / nLocations);
}

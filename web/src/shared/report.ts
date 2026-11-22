import { ref, watch, type Ref } from "vue";

export type Stat = { value: number; lo: number; hi: number };

export type DesignReport = {
  name: string;
  label: string;
  about?: string;
  ablation?: boolean;
  stats: Record<string, Stat>;
  totals: { outages: number; detected: number; true_pages: number; false_pages: number; posts: number; bad_claims: number };
};

export type Gate = { name: string; rule: string; want: number; got: number; pass: boolean };

export type Example = { seed: number; text: string; injected: string[] | null; violations: string[] | null };

export type LaneEvent = {
  offset: number;
  kind: "page" | "ack" | "post" | "refused";
  who?: string;
  level?: string;
  text?: string;
  about: boolean;
  bad?: boolean;
  fallback?: boolean;
  why?: string;
};

export type Lane = { design: string; label: string; events: LaneEvent[] | null };

export type Showcase = {
  seed: number;
  outage: { id: string; service: string; kind: string; start: string; end: string; affected: string[]; public: string[] | null; location?: string };
  series: { offset: number; failing: Record<string, number>; runs: number }[];
  lanes: Lane[];
};

export type Report = {
  world: string;
  seeds: number[];
  days: number;
  checks: number;
  locations: number;
  services: number;
  designs: DesignReport[];
  gates: Gate[];
  checker: {
    drafts: number;
    with_error: number;
    caught: number;
    clean_rejected: number;
    recall: number;
    false_reject_rate: number;
    by_kind: { kind: string; injected: number; caught: number }[];
    refused: Example[] | null;
    through: Example[] | null;
  };
  showcase: Showcase;
  truth: { outages: number; blips: number; night_share: number; median_min: number; by_kind: Record<string, number> };
};

export type World = "dev" | "heldout";

export type LoadState =
  | { status: "loading" }
  | { status: "ready"; report: Report }
  | { status: "missing" }
  | { status: "error"; message: string };

export async function fetchReport(world: World, base = import.meta.env.BASE_URL): Promise<LoadState> {
  try {
    const res = await fetch(`${base}reports/${world}.json`);
    if (res.status === 404) return { status: "missing" };
    if (!res.ok) return { status: "error", message: `The report answered ${res.status}. Rebuild the console with npm run build.` };
    const report = (await res.json()) as Report;
    if (!report.designs?.length) return { status: "missing" };
    return { status: "ready", report };
  } catch {
    return { status: "error", message: "The report could not be read. Run the study, then npm run build." };
  }
}

export function useReport(world: Ref<World>) {
  const state = ref<LoadState>({ status: "loading" });
  watch(
    world,
    async (w) => {
      state.value = { status: "loading" };
      state.value = await fetchReport(w);
    },
    { immediate: true },
  );
  return state;
}

export const MAIN_DESIGNS = ["status_quo", "double_check", "model_only", "desk"];

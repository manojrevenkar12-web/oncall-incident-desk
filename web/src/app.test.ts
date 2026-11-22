import { describe, expect, it, vi, afterEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import App from "./App.vue";
import Scoreboard from "./features/scoreboard/Scoreboard.vue";
import CheckerPanel from "./features/checker/CheckerPanel.vue";
import ReplayTimeline from "./features/replay/ReplayTimeline.vue";
import StateBlock from "./shared/StateBlock.vue";
import { groups, summary } from "./features/replay/replay";
import { fetchReport, type Report } from "./shared/report";
import fixture from "./testing/report.fixture.json";

const report = fixture as unknown as Report;

afterEach(() => vi.unstubAllGlobals());

describe("replay", () => {
  it("folds same-minute events into one marker with a count", () => {
    const lane = report.showcase.lanes.find((l) => l.design === "status_quo")!;
    const gs = groups(lane);
    const pages = gs.filter((g) => g.kind === "page");
    expect(pages.reduce((n, g) => n + g.events.length, 0)).toBe(lane.events!.filter((e) => e.kind === "page").length);
    expect(pages.some((g) => g.events.length > 1)).toBe(true);
  });

  it("summarises who was paged and when someone answered", () => {
    const desk = summary(report.showcase.lanes.find((l) => l.design === "desk")!);
    expect(desk.pages).toBeGreaterThan(0);
    expect(desk.firstPost).not.toBeNull();
  });

  it("writes the headline from the outage itself", () => {
    const w = mount(ReplayTimeline, { props: { showcase: report.showcase, designs: report.designs } });
    expect(w.find("h1").text()).toContain(report.showcase.outage.service);
    expect(w.findAll(".lane").length).toBe(2);
  });
});

describe("scoreboard", () => {
  it("lists every design and marks the desk", () => {
    const w = mount(Scoreboard, { props: { report } });
    expect(w.findAll("tbody tr").length).toBe(report.designs.length);
    expect(w.find("tr.ours").text()).toContain("The desk");
  });
});

describe("checker", () => {
  it("marks the words a refused draft was refused for", () => {
    const w = mount(CheckerPanel, { props: { report } });
    expect(w.findAll("mark").length).toBeGreaterThan(0);
  });
});

describe("states", () => {
  it("tells how to run a missing held-out study", () => {
    const w = mount(StateBlock, { props: { kind: "missing", world: "heldout" } });
    expect(w.text()).toContain("make heldout");
  });

  it("reports a missing report as missing, and a broken one as an error", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })));
    expect((await fetchReport("heldout", "/")).status).toBe("missing");
    vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("offline"); }));
    expect((await fetchReport("dev", "/")).status).toBe("error");
  });

  it("shows the loading state, then the report", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(report), { status: 200 })));
    const w = mount(App);
    expect(w.text()).toContain("Reading the dev report");
    await flushPromises();
    expect(w.text()).toContain("Over 20 simulated weeks");
  });
});

import { useEffect, useState, type ReactNode } from "react";
import { api, type EventDetail, type EventRow, type Funnel } from "../lib/api";
import { useData } from "../lib/data";
import { Sparkline } from "../components/AnalyticsCharts";
import { Calendar, Close, Plus } from "../components/Icons";
import { Menu } from "../components/Menu";
import { Problem, Skeleton } from "../components/ui";
import { toast } from "../components/Toast";

// Events are what a project's apps say happened: a name, maybe a number,
// and properties to split by. This view lists them, opens one over time,
// and follows people through a funnel of steps.

const ranges = [
  { key: "24h", label: "Last 24 hours", ms: 86_400_000 },
  { key: "7d", label: "Last 7 days", ms: 7 * 86_400_000 },
  { key: "30d", label: "Last 30 days", ms: 30 * 86_400_000 },
  { key: "90d", label: "Last 90 days", ms: 90 * 86_400_000 },
];
const envs = [
  { key: "production", label: "Production" },
  { key: "preview", label: "Previews" },
  { key: "", label: "All environments" },
];
const colours = [
  "var(--d-blue)",
  "#d55181",
  "#9085e9",
  "#199e70",
  "var(--d-faint)",
];
const VISIT = "$visit";

export function Events({
  project,
  tabs,
}: {
  project: string;
  tabs: (right: ReactNode) => ReactNode;
}) {
  const [range, setRange] = useState(ranges[0]);
  const [env, setEnv] = useState(envs[0]);
  const [to, setTo] = useState(() => Date.now());
  const [chosen, setChosen] = useState<string | null>(null);
  useEffect(() => {
    const t = window.setInterval(() => setTo(Date.now()), 60_000);
    return () => window.clearInterval(t);
  }, []);
  const q = new URLSearchParams({
    from: String(to - range.ms - ((to - range.ms) % 60_000)),
    to: String(to - (to % 60_000) + 60_000),
  });
  if (env.key) q.set("env", env.key);
  const base = `/api/projects/${encodeURIComponent(project)}/events`;
  const list = useData<{ events: EventRow[] }>(
    `${base}?${q}`,
    () => true,
    30_000,
  );
  const rows = list.data?.events ?? [];
  const name =
    chosen && rows.some((r) => r.name === chosen) ? chosen : rows[0]?.name;
  const right = (
    <>
      <Menu
        label="Environment"
        align="end"
        className="btn quiet medium"
        items={envs.map((e) => ({ label: e.label, onSelect: () => setEnv(e) }))}
      >
        {env.key === "production" && (
          <span className="a-dot" style={{ background: "var(--d-pink)" }} />
        )}
        {env.key === "preview" && (
          <span className="a-dot" style={{ background: "var(--d-violet)" }} />
        )}
        {env.label}
      </Menu>
      <Menu
        label="Time range"
        align="end"
        className="btn quiet medium"
        items={ranges.map((r) => ({
          label: r.label,
          onSelect: () => setRange(r),
        }))}
      >
        <Calendar />
        {range.label}
      </Menu>
    </>
  );
  return (
    <main className="main" style={{ gap: 28 }}>
      {tabs(right)}
      {list.error ? (
        <Problem>{list.error.message}</Problem>
      ) : !list.data ? (
        <div className="panel">
          <Skeleton rows={4} tall />
        </div>
      ) : rows.length === 0 ? (
        <>
          <div className="panel a-empty">
            <div className="t-small">No events yet</div>
            <div className="t-caption t-muted">
              Your apps send their own: an order, a signup, a search. They show
              here as they come in.
            </div>
          </div>
          <SendOne />
        </>
      ) : (
        <>
          <section className="a-section">
            <div className="a-section-head">
              <h2>
                Events<span>sent by your apps</span>
              </h2>
            </div>
            <div
              className="table ev-table"
              style={
                {
                  "--cols": "1.4fr 0.7fr 0.7fr 0.9fr 1.2fr",
                } as React.CSSProperties
              }
            >
              <div className="t-head">
                <span>Event</span>
                <span className="t-right">Count</span>
                <span className="t-right">Visitors</span>
                <span className="t-right">Value</span>
                <span>Over the range</span>
              </div>
              {rows.map((r) => (
                <button
                  key={r.name}
                  className={"t-row" + (r.name === name ? " on" : "")}
                  onClick={() => setChosen(r.name)}
                >
                  <span className="mono a-name">{r.name}</span>
                  <span className="t-right mono">
                    {r.count.toLocaleString()}
                  </span>
                  <span className="t-right mono t-muted">
                    {r.visitors.toLocaleString()}
                  </span>
                  <span
                    className={
                      "t-right mono" + (r.value === null ? " t-quiet" : "")
                    }
                  >
                    {r.value === null ? "None" : number(r.value)}
                  </span>
                  <Sparkline values={r.trend} height={22} />
                </button>
              ))}
            </div>
          </section>
          {name && (
            <Detail
              key={name + env.key + range.key}
              base={base}
              q={q}
              name={name}
            />
          )}
          <div className="a-grid2">
            <FunnelView base={base} q={q} />
            <SendOne />
          </div>
        </>
      )}
    </main>
  );
}

function Detail({
  base,
  q,
  name,
}: {
  base: string;
  q: URLSearchParams;
  name: string;
}) {
  const [by, setBy] = useState<string | null>(null);
  const [measure, setMeasure] = useState<"count" | "value">("count");
  const p = new URLSearchParams(q);
  p.set("name", name);
  if (by) p.set("by", by);
  const d = useData<EventDetail>(
    `${base}/detail?${p}`,
    () => true,
    30_000,
  ).data;
  useEffect(() => {
    if (d && by === null && d.props.length)
      setBy(d.props.includes("plan") ? "plan" : d.props[0]);
  }, [d, by]);
  if (!d) {
    return (
      <div className="panel">
        <Skeleton rows={4} />
      </div>
    );
  }
  const hasValue = d.value !== null;
  const m = hasValue ? measure : "count";
  const totals = d.steps.map((_, i) =>
    d.series.reduce(
      (n, s) => n + (m === "count" ? s.counts[i] : s.values[i]),
      0,
    ),
  );
  const max = Math.max(1, ...totals);
  const day = d.step >= 86_400_000;
  return (
    <div className="ev-detail">
      <section className="a-section">
        <div className="a-section-head">
          <h2>
            <span
              className="mono"
              style={{ color: "var(--d-text)", fontSize: 15 }}
            >
              {name}
            </span>
            <span>{by ? `by ${by}` : "over the range"}</span>
          </h2>
          {hasValue && (
            <div className="seg small">
              {(["count", "value"] as const).map((x) => (
                <button
                  key={x}
                  className={m === x ? "on" : ""}
                  onClick={() => setMeasure(x)}
                >
                  {x === "count" ? "Count" : "Value"}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="panel a-chart-panel">
          <div className="a-legend" style={{ marginBottom: 14 }}>
            {d.breakdown.map((b, i) => (
              <span key={b.key}>
                <span className="a-dot" style={{ background: colours[i] }} />
                {b.key || (by ? "none" : name)}
                <b>
                  {m === "count"
                    ? b.count.toLocaleString()
                    : number(b.value ?? 0)}
                </b>
              </span>
            ))}
          </div>
          <div className="ev-bars">
            {d.steps.map((at, i) => (
              <div
                key={at}
                className="ev-bar"
                title={`${when(at, day)}: ${m === "count" ? totals[i].toLocaleString() : number(totals[i])}`}
              >
                {d.series.map((s, k) => {
                  const v = m === "count" ? s.counts[i] : s.values[i];
                  return v ? (
                    <span
                      key={s.key}
                      style={{
                        height: `${(v / max) * 100}%`,
                        background: colours[k],
                      }}
                    />
                  ) : null;
                })}
              </div>
            ))}
          </div>
          <div className="a-x" style={{ gridColumn: "auto" }}>
            {[0, 6, 12, 18, 23].map((i) => (
              <span key={i}>{d.steps[i] ? when(d.steps[i], day) : ""}</span>
            ))}
          </div>
        </div>
      </section>
      <section className="a-section">
        <div className="a-section-head">
          <h2>Split by</h2>
          {d.props.length > 0 && (
            <Menu
              label="Split by"
              align="end"
              className="btn quiet medium"
              items={[
                { label: "Nothing", onSelect: () => setBy("") },
                ...d.props.map((k) => ({ label: k, onSelect: () => setBy(k) })),
              ]}
            >
              <span className="mono">{by || "nothing"}</span>
            </Menu>
          )}
        </div>
        {d.props.length > 0 && (
          <div
            className="table"
            style={{ "--cols": "1fr 0.6fr 0.9fr" } as React.CSSProperties}
          >
            <div className="t-head">
              <span>{by || "All"}</span>
              <span className="t-right">Count</span>
              <span className="t-right">Value</span>
            </div>
            {d.breakdown.map((b, i) => (
              <div key={b.key} className="t-row">
                <span className="ev-key">
                  <span className="a-dot" style={{ background: colours[i] }} />
                  <span className="mono a-name">
                    {b.key || (by ? "none" : name)}
                  </span>
                </span>
                <span className="t-right mono">{b.count.toLocaleString()}</span>
                <span className="t-right mono t-muted">
                  {b.value === null ? "None" : number(b.value)}
                </span>
              </div>
            ))}
            {d.breakdown.length > 1 && (
              <div className="t-row ev-total">
                <span>All</span>
                <span className="t-right mono">{d.count.toLocaleString()}</span>
                <span className="t-right mono">
                  {d.value === null ? "None" : number(d.value)}
                </span>
              </div>
            )}
          </div>
        )}
        {d.props.length === 0 && (
          <div className="panel ev-none t-caption t-muted">
            These events have no properties to split by. Send some, such as a
            plan or a country, to compare them here.
          </div>
        )}
      </section>
    </div>
  );
}

function FunnelView({ base, q }: { base: string; q: URLSearchParams }) {
  const f = useData<Funnel>(`${base}/funnel?${q}`, () => true, 30_000);
  // A funnel needs two steps; one chosen so far waits here until the next.
  const [draft, setDraft] = useState<string[] | null>(null);
  const d = f.data;
  const steps = draft ?? d?.steps ?? [];
  const change = async (next: string[]) => {
    if (next.length === 1) {
      setDraft(next);
      if (d?.steps.length) next = [];
      else return;
    } else {
      setDraft(null);
    }
    try {
      await api(`${base}/funnel`, {
        method: "PUT",
        body: JSON.stringify({ steps: next }),
      });
      f.reload();
    } catch (e) {
      toast(e instanceof Error ? e.message : "Couldn't save the funnel", {
        tone: "failed",
      });
    }
  };
  const label = (s: string) => (s === VISIT ? "Visited a page" : s);
  const choices = d
    ? [VISIT, ...d.names].filter((n) => !steps.includes(n))
    : [];
  const counted = !draft && d && d.steps.length === d.counts.length;
  return (
    <section className="a-section">
      <div className="a-section-head">
        <h2>Funnel</h2>
        {d && steps.length < 5 && choices.length > 0 && (
          <Menu
            label="Add a step"
            align="end"
            className="btn quiet medium"
            items={choices.map((n) => ({
              label: label(n),
              onSelect: () => change([...steps, n]),
            }))}
          >
            <Plus />
            Step
          </Menu>
        )}
      </div>
      <div className="panel ev-funnel">
        {!d ? (
          <Skeleton rows={3} />
        ) : steps.length === 0 ? (
          <div className="t-caption t-muted">
            Choose the steps people take, such as visiting, starting checkout
            and ordering, to see how many get through each.
          </div>
        ) : (
          steps.map((s, i) => {
            const n = counted ? (d.counts[i] ?? 0) : 0;
            const first = (counted && d.counts[0]) || 1;
            const prev = (counted && i && d.counts[i - 1]) || 1;
            return (
              <div key={s} className="ev-step">
                <div className="ev-step-head">
                  <span className={s === VISIT ? "" : "mono"}>{label(s)}</span>
                  <span>
                    {counted && (
                      <span className="mono">{n.toLocaleString()}</span>
                    )}
                    {counted && i > 0 && (
                      <span className="t-muted">
                        , {Math.round((n / prev) * 100)}%
                      </span>
                    )}
                    <button
                      className="ev-remove"
                      aria-label={`Remove ${label(s)}`}
                      onClick={() => change(steps.filter((x) => x !== s))}
                    >
                      <Close />
                    </button>
                  </span>
                </div>
                <div className="ev-step-bar">
                  <span
                    style={{ width: `${counted ? (n / first) * 100 : 0}%` }}
                  />
                </div>
              </div>
            );
          })
        )}
        {draft && (
          <div className="ev-funnel-foot">Add one more step to count it</div>
        )}
        {counted && d.steps.length > 1 && (
          <div className="ev-funnel-foot">
            {(
              d.counts[d.counts.length - 2] - d.counts[d.counts.length - 1]
            ).toLocaleString()}{" "}
            reached {label(d.steps[d.steps.length - 2])} and stopped there
          </div>
        )}
      </div>
    </section>
  );
}

function SendOne() {
  return (
    <section className="a-section">
      <div className="a-section-head">
        <h2>Send an event</h2>
      </div>
      <div className="panel ev-code">
        <div className="ev-comment">
          // from the server: every deploy has these two variables
        </div>
        <div>
          <span className="ev-k">await</span> fetch(process.env.
          <span className="ev-v">PIER_EVENTS_URL</span>, {"{"}
        </div>
        <div className="ev-in">
          method: <span className="ev-s">'POST'</span>,
        </div>
        <div className="ev-in">
          headers: {"{"} authorization:{" "}
          <span className="ev-s">
            {"`Bearer ${process.env.PIER_EVENTS_TOKEN}`"}
          </span>{" "}
          {"}"},
        </div>
        <div className="ev-in">
          body: JSON.stringify({"{"} name: <span className="ev-s">'order'</span>
          , value: 42.5, props: {"{"} plan: <span className="ev-s">'pro'</span>{" "}
          {"}"} {"}"}),
        </div>
        <div>{"})"}</div>
        <div className="ev-gap" />
        <div className="ev-comment">
          // from the browser, to the app's own address
        </div>
        <div>
          navigator.sendBeacon(<span className="ev-s">'/_pier/event'</span>,
          JSON.stringify({"{"} name: <span className="ev-s">'search'</span>{" "}
          {"}"}))
        </div>
      </div>
    </section>
  );
}

function number(v: number) {
  return v.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

function when(ms: number, day: boolean) {
  const d = new Date(ms);
  return day
    ? d.toLocaleDateString(undefined, { day: "numeric", month: "short" })
    : d.toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
}

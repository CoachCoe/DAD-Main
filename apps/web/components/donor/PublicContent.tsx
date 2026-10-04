"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Initiative = { slug: string; title: string; summary: string; partner: string; planned_start: string };
type Content = { state: "unpublished" | "published"; initiatives: Initiative[];
  ledger: { metric: string; value: number; unit: string; illustrative: boolean }[]; ledger_as_of: string | null; cadence: string | null };
export function PublicContent({ view, slug }: { view: "initiatives" | "ledger"; slug?: string }) {
  const [data, setData] = useState<Content | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setData(null); setError("");
    api<Content>("/donor/public/content").then((value) => { if (active) setData(value); }).catch(() => {
      if (active) setError("This page could not load its published records. Check your connection and retry.");
    });
    return () => { active = false; };
  }, [retry]);
  if (error) return <section className="panel"><p role="alert">{error}</p><button onClick={() => setRetry((value) => value + 1)}>Retry records</button></section>;
  if (!data) return <p role="status">Loading published records…</p>;
  if (view === "ledger") return <section className="panel">
    {data.ledger_as_of && <p>As of <time dateTime={data.ledger_as_of}>{data.ledger_as_of}</time> · Updated {data.cadence}.</p>}
    {data.ledger.length ? <div className="donorMetrics">{data.ledger.map((item, index) => <article key={index}>
      <h2>{item.metric}</h2><p className="donorMetricValue">{item.unit === "USD_CENTS" ? new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(item.value / 100) : `${item.value.toLocaleString()} ${item.unit.toLowerCase()}`}</p>
      <p>{item.illustrative ? <strong>Illustrative figure — not a reported outcome</strong> : "Source document retained by DAD and available on request."}</p>
    </article>)}</div> : <p>No ledger figures have been published yet. Figures will appear with their source records and an as-of date.</p>}
  </section>;
  const selected = slug ? data.initiatives.filter((item) => item.slug === slug) : data.initiatives;
  return <section className="panel">{selected.length ? selected.map((item) => <article key={item.slug}>
    <h2>{slug ? item.title : <Link href={`/initiatives/${item.slug}`}>{item.title}</Link>}</h2>
    <p>{item.summary}</p><p>Partner: {item.partner}. Planned start: <time dateTime={item.planned_start}>{item.planned_start}</time>.</p>
    <p>Planned initiative. Programs open at launch; this is not a recipient enrolment form.</p>
    {slug && <p><Link href="/donate">Support DAD’s work</Link> — gifts support DAD as a whole and are not designated to this initiative.</p>}
  </article>) : <p>{slug ? "This initiative has not been published. Browse the initiatives index for current plans." : "Board-approved plans will appear here as they are published. No initiatives have been published yet."}</p>}</section>;
}

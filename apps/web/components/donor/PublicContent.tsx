import Link from "next/link";
import type { Initiative, PublicContent } from "@/lib/publicContent";
import { formatMoney } from "@/lib/money";
import { RefreshButton } from "./RefreshButton";

export function ContentUnavailable() {
  return <div className="errorMessage" role="alert">
    <b>Published records are unavailable right now.</b>
    <span>This page shows only figures DAD has published, so nothing is shown in their place. Please retry in a moment.</span>
    <RefreshButton>Retry records</RefreshButton>
  </div>;
}

export function Ledger({ content }: { content: PublicContent }) {
  return <>
    {content.ledger_as_of && <p>As of <time dateTime={content.ledger_as_of}>{content.ledger_as_of}</time> · Updated {content.cadence}.</p>}
    {content.ledger.length ? <div className="donorMetrics">{content.ledger.map((item) => <article key={item.id}>
      <h2>{item.metric}</h2><p className="donorMetricValue">{item.unit === "USD_CENTS" ? formatMoney(item.value) : `${item.value.toLocaleString("en-US")} ${item.unit.toLowerCase()}`}</p>
      <p>{item.illustrative ? <strong>Illustrative figure — not a reported outcome</strong> : "Source document retained by DAD and available on request."}</p>
    </article>)}</div> : <p>No ledger figures have been published yet. Figures will appear with their source records and an as-of date.</p>}
  </>;
}

export function InitiativeList({ initiatives }: { initiatives: Initiative[] }) {
  if (!initiatives.length) return <p>Board-approved plans will appear here as they are published. No initiatives have been published yet.</p>;
  return initiatives.map((item) => <article key={item.slug}>
    <h2><Link href={`/initiatives/${item.slug}`}>{item.title}</Link></h2>
    <InitiativeFacts initiative={item} />
  </article>);
}

export function InitiativeFacts({ initiative }: { initiative: Initiative }) {
  return <>
    <p>{initiative.summary}</p>
    <p>Partner: {initiative.partner}. Planned start: <time dateTime={initiative.planned_start}>{initiative.planned_start}</time>.</p>
    <p>Planned initiative. Programs open at launch; this is not a recipient enrolment form.</p>
  </>;
}

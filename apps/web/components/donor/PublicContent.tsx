import Link from "next/link";
import type { Schema } from "@/lib/api";
import { formatDay } from "@/lib/dates";
import { formatMoney } from "@/lib/money";
import { RefreshButton } from "./RefreshButton";

export function ContentUnavailable() {
  return <div className="errorMessage" role="alert">
    <b>Published records are unavailable right now.</b>
    <span>This page shows only figures DAD has published, so nothing is shown in their place. Please retry in a moment.</span>
    <RefreshButton>Retry records</RefreshButton>
  </div>;
}

const counted: Record<Exclude<Schema<"LedgerFigure">["unit"], "USD_CENTS">, [string, string]> = {
  PEOPLE: ["person", "people"], INITIATIVES: ["initiative", "initiatives"], PARTNERS: ["partner", "partners"],
};

function figure({ value, unit }: Schema<"LedgerFigure">) {
  if (unit === "USD_CENTS") return formatMoney(value);
  return `${value.toLocaleString("en-US")} ${counted[unit][value === 1 ? 0 : 1]}`;
}

export function Ledger({ content }: { content: Schema<"PublicContent"> }) {
  return <>
    {content.ledger_as_of && <p className="note">As of <time dateTime={content.ledger_as_of}>{formatDay(content.ledger_as_of)}</time> · Updated {content.cadence}.</p>}
    {content.ledger.length ? <div className="metrics">{content.ledger.map((item) => <article key={item.id} className={item.illustrative ? "metric illustrative" : "metric"}>
      <h2>{item.metric}</h2>
      {/* The label sits on the figure itself, so no crop or screenshot can separate them. */}
      <p className="metricValue">{figure(item)}
        {item.illustrative && <span className="badge">Illustrative</span>}</p>
      <p>{item.illustrative ? <strong>Illustrative figure — not a reported outcome</strong> : "Source document retained by DAD and available on request."}</p>
    </article>)}</div> : <p className="callout">No ledger figures have been published yet. Figures will appear with their source records and an as-of date.</p>}
  </>;
}

export function InitiativeList({ initiatives }: { initiatives: Schema<"Initiative">[] }) {
  if (!initiatives.length) return <p className="callout">Board-approved plans will appear here as they are published. No initiatives have been published yet.</p>;
  return <div className="metrics">{initiatives.map((item) => <article key={item.slug} className="metric">
    <h2 className="initiativeTitle"><Link href={`/initiatives/${item.slug}`}>{item.title}</Link></h2>
    <InitiativeFacts initiative={item} />
  </article>)}</div>;
}

export function InitiativeFacts({ initiative }: { initiative: Schema<"Initiative"> }) {
  return <>
    <p>{initiative.summary}</p>
    <p>Partner: {initiative.partner}. Planned start: <time dateTime={initiative.planned_start}>{formatDay(initiative.planned_start)}</time>.</p>
    <p>Planned initiative. Programs open at launch; this is not a recipient enrolment form.</p>
  </>;
}

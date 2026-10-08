"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import type { Schema } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { formatDateTime } from "@/lib/dates";

export default function Audit() {
  const [offset, setOffset] = useState(0);
  const { resource, data, reload } = useApiResource<Schema<"AuditPage">>(`/donor/staff/audit?offset=${offset}`);
  const messages = resourceMessages(resource, { loading: "Loading audit history…",
    failure: "Audit history could not be loaded right now. Please retry in a moment.",
    forbidden: "Audit history requires administrator access." });
  return <Page wide eyebrow="Staff" back={{ href: "/staff", label: "Staff back office" }} title="Staff audit history">
    {resource.state === "signedOut" && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {data && <>{data.events.length ? <ol>{data.events.map((event) => <li key={event.id}>
      <time dateTime={event.at}>{formatDateTime(event.at)}</time> · {event.action}
      <p>Actor: {event.actor_id || "No authenticated actor (bootstrap or failed sign-in)"}. Target: {event.target}</p>
      {Object.keys(event.details).length > 0 && <dl className="details">{Object.entries(event.details).map(([key, value]) => <div key={key}>
        <dt>{key.replaceAll("_", " ")}</dt><dd>{typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? String(value) : JSON.stringify(value)}</dd>
      </div>)}</dl>}
    </li>)}</ol> : <p>No events recorded.</p>}
      {offset > 0 && <button type="button" className="secondary" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer events</button>}
      {data.next_offset !== null && <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>Older events</button>}
    </>}
    <Announcer status={messages.status} alert={messages.alert} />
    {resource.state !== "signedOut" && <button type="button" className="secondary" onClick={reload}>Refresh audit history</button>}
  </Page>;
}

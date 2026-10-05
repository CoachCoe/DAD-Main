"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Events = { events: { id: string; actor_id: string | null; action: string; target: string; at: string; details: Record<string, unknown> }[]; next_offset: number | null };

export default function Audit() {
  const [offset, setOffset] = useState(0);
  const { resource, data, reload } = useApiResource<Events>(`/donor/staff/audit?offset=${offset}`);
  const messages = resourceMessages(resource, { loading: "Loading audit history…",
    failure: "Audit history could not be loaded right now. Please retry in a moment.",
    forbidden: "Audit history requires administrator access." });
  return <section className="panel"><h1>Staff audit history</h1><p><Link href="/staff">Staff back office</Link></p>
    {resource.state === "signedOut" && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {data && <>{data.events.length ? <ol>{data.events.map((event) => <li key={event.id}>
      <time dateTime={event.at}>{new Date(event.at).toLocaleString()}</time> · {event.action}
      <p>Actor: {event.actor_id || "No authenticated actor (bootstrap or failed sign-in)"}. Target: {event.target}</p>
      <pre>{JSON.stringify(event.details, null, 2)}</pre>
    </li>)}</ol> : <p>No events recorded.</p>}
      {offset > 0 && <button type="button" className="secondary" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer events</button>}
      {data.next_offset !== null && <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>Older events</button>}
    </>}
    <Announcer status={messages.status} alert={messages.alert} />
    {resource.state !== "signedOut" && <button type="button" className="secondary" onClick={reload}>Refresh audit history</button>}
  </section>;
}

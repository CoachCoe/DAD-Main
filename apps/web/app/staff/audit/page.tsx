"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Audit = { events: { id: string; actor_id: string | null; action: string; target: string; at: string; details: Record<string, unknown> }[]; next_offset: number | null };
export default function AuditPage() {
  const [data, setData] = useState<Audit | null>(null);
  const [offset, setOffset] = useState(0);
  const [retry, setRetry] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    setError(""); setData(null);
    api<Audit>(`/donor/staff/audit?offset=${offset}`, { cache: retry > 0 ? "reload" : "default" }).then((value) => { if (active) setData(value); }).catch(() => {
      if (active) setError("Audit history is unavailable. Administrator access is required; check your sign-in and connection.");
    });
    return () => { active = false; };
  }, [offset, retry]);
  return <section className="panel"><h1>Staff audit history</h1><p><Link href="/staff">Staff back office</Link></p>
    {!data && !error && <p role="status">Loading audit history…</p>}
    {data && <>{data.events.length ? <ol>{data.events.map((event) => <li key={event.id}>
      <time dateTime={event.at}>{new Date(event.at).toLocaleString()}</time> · {event.action}
      <p>Actor: {event.actor_id || "No authenticated actor (bootstrap or failed sign-in)"}. Target: {event.target}</p>
      <pre>{JSON.stringify(event.details, null, 2)}</pre>
    </li>)}</ol> : <p>No events recorded.</p>}
      {offset > 0 && <button type="button" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer events</button>}
      {data.next_offset !== null && <button type="button" onClick={() => setOffset(data.next_offset ?? 0)}>Older events</button>}
    </>}{error && <p role="alert">{error}</p>}<button type="button" onClick={() => setRetry((value) => value + 1)}>Refresh audit history</button>
  </section>;
}

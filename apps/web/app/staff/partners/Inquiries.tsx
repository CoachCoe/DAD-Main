"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { formatDateTime } from "@/lib/dates";

type Inquiries = { signups: { id: string; organization: string; name: string; email: string; website: string; message: string; at: string }[]; next_offset: number | null };

export default function Inquiries() {
  const [offset, setOffset] = useState(0);
  const { resource, data, reload } = useApiResource<Inquiries>(`/donor/staff/partners?offset=${offset}`);
  const messages = resourceMessages(resource, { loading: "Loading inquiries…",
    failure: "Partner inquiries could not be loaded right now. Please retry in a moment.",
    forbidden: "Partner inquiries require administrator access." });
  return <Page wide eyebrow="Staff" back={{ href: "/staff", label: "Staff back office" }} title="Partner inquiries"><p><Link href="/staff/exports">Export inquiries</Link></p>
    {resource.state === "signedOut" && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {data && <>{data.signups.length ? data.signups.map((item) => <article key={item.id}><h2>{item.organization}</h2>
      <p>{item.name} · {item.email} · {formatDateTime(item.at)}</p><p>{item.website}</p><p>{item.message}</p>
    </article>) : <p>No partner inquiries recorded.</p>}
      {offset > 0 && <button type="button" className="secondary" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer inquiries</button>}
      {data.next_offset !== null && <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>Older inquiries</button>}
    </>}
    <Announcer status={messages.status} alert={messages.alert} />
    {resource.state !== "signedOut" && <button type="button" className="secondary" onClick={reload}>Refresh inquiries</button>}
  </Page>;
}

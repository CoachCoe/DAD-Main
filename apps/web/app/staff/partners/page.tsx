"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Inquiries = { signups: { id: string; organization: string; name: string; email: string; website: string; message: string; at: string }[]; next_offset: number | null };
export default function PartnerInquiries() {
  const [data, setData] = useState<Inquiries | null>(null);
  const [offset, setOffset] = useState(0);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true; setError(""); setData(null);
    api<Inquiries>(`/donor/staff/partners?offset=${offset}`, { cache: retry > 0 ? "reload" : "default" }).then((value) => { if (active) setData(value); }).catch(() => { if (active) setError("Partner inquiries require administrator access. Check sign-in and connection."); });
    return () => { active = false; };
  }, [offset, retry]);
  return <section className="panel"><h1>Partner inquiries</h1><p><Link href="/staff">Staff back office</Link> · <Link href="/staff/exports">Export inquiries</Link></p>
    {!data && !error && <p role="status">Loading inquiries…</p>}
    {data && <>{data.signups.length ? data.signups.map((item) => <article key={item.id}><h2>{item.organization}</h2>
      <p>{item.name} · {item.email} · {new Date(item.at).toLocaleString()}</p><p>{item.website}</p><p>{item.message}</p>
    </article>) : <p>No partner inquiries recorded.</p>}
      {offset > 0 && <button type="button" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer inquiries</button>}
      {data.next_offset !== null && <button type="button" onClick={() => setOffset(data.next_offset ?? 0)}>Older inquiries</button>}
    </>}{error && <p role="alert">{error}</p>}<button type="button" onClick={() => setRetry((value) => value + 1)}>Refresh inquiries</button>
  </section>;
}

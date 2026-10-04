"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Messages = { messages: { id: string; kind: string; attempts: number; completed_at: string | null; last_error: string | null; suppressed_reason: string | null; available_at: string }[]; next_offset: number | null };
export default function MessagesPage() {
  const [donor, setDonor] = useState("");
  const [data, setData] = useState<Messages | null>(null);
  const [offset, setOffset] = useState(0);
  const [retry, setRetry] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const keys = useRef<Record<string, string>>({});
  useEffect(() => { setDonor(new URL(window.location.href).searchParams.get("donor") || ""); }, []);
  useEffect(() => {
    if (!donor) return;
    let active = true;
    setError(""); setData(null);
    api<Messages>(`/donor/staff/donors/${encodeURIComponent(donor)}/messages?offset=${offset}`).then((value) => { if (active) setData(value); }).catch(() => {
      if (active) setError("Delivery history is unavailable. Check your staff access and connection.");
    });
    return () => { active = false; };
  }, [donor, offset, retry]);
  async function resend(id?: string) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      if (id) {
        keys.current[id] ??= crypto.randomUUID();
        await api(`/donor/staff/messages/${id}/resend`, { method: "POST", headers: { "Idempotency-Key": keys.current[id] } });
      } else await api(`/donor/staff/donors/${encodeURIComponent(donor)}/claim-email`, { method: "POST" });
      setMessage("Delivery request recorded. This does not confirm email delivery.");
      setRetry((value) => value + 1);
    } catch { setError("Resend is not confirmed. Check whether the original is still queued, wait one minute, then retry the same request."); }
    finally { setBusy(false); }
  }
  return <section className="panel"><h1>Donor delivery history</h1><p><Link href="/staff">Staff back office</Link> · <Link href="/staff/sign-in">Staff sign-in</Link></p>
    {!donor && <p>Select a donor in the back office to view their messages.</p>}
    {donor && !data && !error && <p role="status">Loading delivery history…</p>}
    {data && <><details><summary>Request account claim email</summary><p>This sends the donor a new account-access link if delivery is available.</p><button disabled={busy} onClick={() => resend()}>Confirm claim email request</button></details>
      {data.messages.length ? <ul>{data.messages.map((item) => <li key={item.id}>
        {item.kind} · {item.suppressed_reason ? `Suppressed: ${item.suppressed_reason}` : item.completed_at ? "Sent" : item.attempts >= 5 ? "Parked for review" : "Queued"} · Attempts {item.attempts}
        {item.last_error && <> · Last failure: {item.last_error}</>}
        {["RECEIPT", "ANNUAL"].includes(item.kind) && !item.suppressed_reason && (item.completed_at || item.attempts >= 5) && <details><summary>Resend record</summary><p>Send a new copy showing current verified refund adjustments.</p><button disabled={busy} onClick={() => resend(item.id)}>Confirm resend</button></details>}
      </li>)}</ul> : <p>No transactional messages recorded.</p>}
      {offset > 0 && <button onClick={() => setOffset(Math.max(0, offset - 50))}>Newer messages</button>}
      {data.next_offset !== null && <button onClick={() => setOffset(data.next_offset!)}>Older messages</button>}
    </>}
    {message && <p role="status">{message}</p>}{error && <p role="alert">{error}</p>}
    <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh delivery history</button>
  </section>;
}

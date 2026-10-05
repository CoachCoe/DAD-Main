"use client";
import { useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, unconfirmedMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Messages = { messages: { id: string; kind: string; attempts: number; completed_at: string | null; last_error: string | null; suppressed_reason: string | null; available_at: string }[]; next_offset: number | null };

export default function DeliveryHistory({ donor }: { donor: string }) {
  const [offset, setOffset] = useState(0);
  const { resource, data, reload } = useApiResource<Messages>(donor ? `/donor/staff/donors/${encodeURIComponent(donor)}/messages?offset=${offset}` : null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const keys = useRef<Record<string, string>>({});
  const messages = resourceMessages(resource, { loading: "Loading delivery history…",
    failure: "Delivery history could not be loaded right now. Please retry in a moment.",
    forbidden: "Your staff role does not permit viewing delivery history." });
  async function resend(id?: string) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      if (id) {
        keys.current[id] ??= crypto.randomUUID();
        await api(`/donor/staff/messages/${id}/resend`, { method: "POST", headers: { "Idempotency-Key": keys.current[id] } });
      } else await api(`/donor/staff/donors/${encodeURIComponent(donor)}/claim-email`, { method: "POST" });
      setMessage("Delivery request recorded. This does not confirm email delivery.");
      reload();
    } catch (cause) { setError(unconfirmedMessage(cause, "Resend is not confirmed. Check whether the original is still queued, wait one minute, then retry the same request.")); }
    finally { setBusy(false); }
  }
  return <section className="panel"><h1>Donor delivery history</h1><p><Link href="/staff">Staff back office</Link></p>
    {!donor && <p>Select a donor in the back office to view their messages.</p>}
    {resource.state === "signedOut" && donor && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {data && <><details><summary>Request account claim email</summary><p>This sends the donor a new account-access link if delivery is available.</p><button type="button" className="secondary" disabled={busy} onClick={() => resend()}>Confirm claim email request</button></details>
      {data.messages.length ? <ul>{data.messages.map((item) => <li key={item.id}>
        {item.kind} · {item.suppressed_reason ? `Suppressed: ${item.suppressed_reason}` : item.completed_at ? "Sent" : item.attempts >= 5 ? "Parked for review" : "Queued"} · Attempts {item.attempts}
        {item.last_error && <> · Last failure: {item.last_error}</>}
        {["RECEIPT", "ANNUAL"].includes(item.kind) && !item.suppressed_reason && (item.completed_at || item.attempts >= 5) && <details><summary>Resend record</summary><p>Send a new copy showing current verified refund adjustments.</p><button type="button" className="secondary" disabled={busy} onClick={() => resend(item.id)}>Confirm resend</button></details>}
      </li>)}</ul> : <p>No transactional messages recorded.</p>}
      {offset > 0 && <button type="button" className="secondary" onClick={() => setOffset(Math.max(0, offset - 50))}>Newer messages</button>}
      {data.next_offset !== null && <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>Older messages</button>}
    </>}
    <Announcer status={donor ? messages.status || message : ""} alert={donor ? error || messages.alert : ""} />
    {donor && resource.state !== "signedOut" && <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh delivery history</button>}
  </section>;
}

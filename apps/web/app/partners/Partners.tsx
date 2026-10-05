"use client";
import { useRef, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, unconfirmedMessage } from "@/lib/api";

const initial = { organization: "", name: "", email: "", website: "", message: "", contact_consent: false };
export default function Partners() {
  const [data, setData] = useState(initial);
  const [frozen, setFrozen] = useState<typeof initial | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const key = useRef<string | null>(null);
  async function submit(event: React.FormEvent) {
    event.preventDefault(); if (busy) return;
    const body = frozen || data;
    setFrozen(body); key.current ??= crypto.randomUUID(); setBusy(true); setError("");
    try { await api("/donor/public/partner-signup", { method: "POST", headers: { "Idempotency-Key": key.current }, body: JSON.stringify(body) }); setDone(true); }
    catch (cause) {
      if (cause instanceof ApiError && [409, 422].includes(cause.status)) { setFrozen(null); key.current = null; setError("Check the form fields and the HTTPS website address before submitting again."); }
      else setError(unconfirmedMessage(cause, "Your submission is not confirmed. Retry the same form."));
    }
    finally { setBusy(false); }
  }
  return <section className="panel"><h1>Partner with DAD</h1><p>Help shape initiatives for our April 2027 launch. This is an interest form; partner accounts arrive at R2.</p>
    <p>Sandbox preview: use test contact details. <Link href="/legal">Legal and privacy status</Link>.</p>
    {!done && <form onSubmit={submit}>
      <fieldset disabled={busy || frozen !== null}><legend>Organization and contact</legend>
        <label>Organization<input value={data.organization} maxLength={200} required onChange={(event) => setData({ ...data, organization: event.target.value })} /></label>
        <label>Your name<input value={data.name} maxLength={100} required onChange={(event) => setData({ ...data, name: event.target.value })} /></label>
        <label>Email<input type="email" value={data.email} maxLength={320} required onChange={(event) => setData({ ...data, email: event.target.value })} /></label>
        <label>Organization website (HTTPS, optional)<input type="url" pattern="https://.*" value={data.website} maxLength={500} onChange={(event) => setData({ ...data, website: event.target.value })} /></label>
        <label>Tell us about your work (optional)<textarea value={data.message} maxLength={2000} onChange={(event) => setData({ ...data, message: event.target.value })} /></label>
        <label><input type="checkbox" checked={data.contact_consent} required onChange={(event) => setData({ ...data, contact_consent: event.target.checked })} /> DAD may contact me about this inquiry. This does not subscribe me to marketing.</label>
      </fieldset>
      <button className="button" disabled={busy} type="submit">{busy ? "Submitting…" : frozen ? "Retry original inquiry" : "Submit partner interest"}</button>
    </form>}
    <Announcer status={done ? "Your test inquiry was received. This does not confirm a partnership or promise funding." : ""} alert={error} />
  </section>;
}

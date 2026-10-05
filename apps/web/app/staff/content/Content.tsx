"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, failureMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Inventory = { sources: { id: string; name: string; sha256: string }[]; publications: { id: string; at: string }[] };

export default function Content() {
  const { resource, data, reload } = useApiResource<Inventory>("/donor/staff/content");
  const [source, setSource] = useState<File | null>(null);
  const [initiatives, setInitiatives] = useState<File | null>(null);
  const [ledger, setLedger] = useState<File | null>(null);
  const [asOf, setAsOf] = useState("");
  const [cadence, setCadence] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const messages = resourceMessages(resource, { loading: "Loading content inventory…",
    failure: "The content inventory could not be loaded right now. Please retry in a moment.",
    forbidden: "Content tools require administrator access." });
  const rejected = (cause: unknown, fallback: string) => cause instanceof ApiError && cause.status === 403
    ? "Your staff role does not permit publishing." : failureMessage(cause, fallback);
  async function upload(event: React.FormEvent) {
    event.preventDefault(); if (!source || busy) return;
    if (source.size > 10 * 1024 * 1024) { setError("Source documents must be at most 10 MB."); return; }
    setBusy(true); setError(""); setMessage("");
    try { const result = await api<{ id: string }>(`/donor/staff/content/sources?name=${encodeURIComponent(source.name)}`, { method: "POST", body: source }); setMessage(`Source stored: ${result.id}. Use this ID in the spreadsheet.`); reload(); }
    catch (cause) { setError(rejected(cause, "Source upload was not confirmed. Check source encryption configuration, then refresh the inventory before retrying.")); }
    finally { setBusy(false); }
  }
  async function publish(event: React.FormEvent) {
    event.preventDefault(); if (!initiatives || !ledger || !confirmed || busy) return;
    if (initiatives.size > 512 * 1024 || ledger.size > 512 * 1024) { setError("Each CSV must be at most 512 KB."); return; }
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<{ publication_id: string }>("/donor/staff/content/publish", { method: "POST", body: JSON.stringify({
        initiatives_csv: await initiatives.text(), ledger_csv: await ledger.text(), as_of: asOf, cadence,
      }) });
      setMessage(`Published snapshot ${result.publication_id}. Public caches refresh within one minute.`); reload(); setConfirmed(false);
    } catch (cause) { setError(rejected(cause, "Publication was not confirmed. Check column headers, uploaded source IDs, dates, units, and illustrative flags; refresh history before retrying.")); }
    finally { setBusy(false); }
  }
  return <section className="panel"><h1>Spreadsheet publication</h1><p><Link href="/staff">Staff back office</Link> · <Link href="/staff/exports">Exports</Link></p>
    <p>Edit approved content in the source spreadsheet, then import its CSV export. This publishes one complete snapshot of planned initiatives and the static ledger.</p>
    {resource.state === "signedOut" && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {data && <>
      <form onSubmit={upload}><fieldset disabled={busy}><legend>Retain a source document</legend><label>Source file<input type="file" onChange={(event) => setSource(event.target.files?.[0] || null)} required /></label><button type="submit" className="secondary">Upload private source</button></fieldset></form>
      <h2>Source inventory</h2>{data.sources.length ? <ul>{data.sources.map((item) => <li key={item.id}><a href={`/api/donor/staff/content/sources/${item.id}`}>{item.name}</a> · ID <code>{item.id}</code> · SHA-256 <code>{item.sha256}</code></li>)}</ul> : <p>No source documents stored.</p>}
      <form onSubmit={publish}><fieldset disabled={busy}><legend>Publish approved spreadsheet snapshot</legend>
        <p>Initiative CSV columns:</p><pre>slug,title,summary,partner,planned_start,board_approval_source_id,approved_on</pre>
        <p>Ledger CSV columns:</p><pre>metric,value,unit,source_id,illustrative</pre>
        <p>Units: USD_CENTS, PEOPLE, INITIATIVES, PARTNERS. Values are nonnegative integers. Illustrative must be true or false; real figures require a stored source ID. Empty sheets must retain their column headers.</p>
        <label>Initiatives CSV<input type="file" accept=".csv,text/csv" onChange={(event) => setInitiatives(event.target.files?.[0] || null)} required /></label>
        <label>Ledger CSV<input type="file" accept=".csv,text/csv" onChange={(event) => setLedger(event.target.files?.[0] || null)} required /></label>
        <label>Ledger as-of date<input type="date" value={asOf} onChange={(event) => setAsOf(event.target.value)} required /></label>
        <label>Update cadence<input maxLength={100} placeholder="For example: monthly" value={cadence} onChange={(event) => setCadence(event.target.value)} required /></label>
        <label><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} required /> I checked board approvals, source documents, illustrative labels, and the as-of date. Publish this snapshot publicly.</label>
        <button type="submit" className="button">Confirm publication</button>
      </fieldset></form>
      <h2>Publication history</h2><ul>{data.publications.map((item) => <li key={item.id}>{new Date(item.at).toLocaleString()} · <code>{item.id}</code></li>)}</ul>
    </>}
    <Announcer status={messages.status || message} alert={error || messages.alert} />
    {resource.state !== "signedOut" && <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh content inventory</button>}
  </section>;
}

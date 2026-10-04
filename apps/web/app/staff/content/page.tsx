"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";

type Inventory = { sources: { id: string; name: string; sha256: string }[]; publications: { id: string; at: string }[] };
export default function ContentPage() {
  const [data, setData] = useState<Inventory | null>(null);
  const [source, setSource] = useState<File | null>(null);
  const [initiatives, setInitiatives] = useState<File | null>(null);
  const [ledger, setLedger] = useState<File | null>(null);
  const [asOf, setAsOf] = useState("");
  const [cadence, setCadence] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setError("");
    api<Inventory>("/donor/staff/content").then((value) => { if (active) setData(value); }).catch(() => { if (active) setError("Content tools require administrator access. Check your sign-in and connection."); });
    return () => { active = false; };
  }, [retry]);
  async function upload(event: React.FormEvent) {
    event.preventDefault(); if (!source || busy) return;
    if (source.size > 10 * 1024 * 1024) { setError("Source documents must be at most 10 MB."); return; }
    setBusy(true); setError(""); setMessage("");
    try { const result = await api<{ id: string }>(`/donor/staff/content/sources?name=${encodeURIComponent(source.name)}`, { method: "POST", body: source }); setMessage(`Source stored: ${result.id}. Use this ID in the spreadsheet.`); setRetry((value) => value + 1); }
    catch { setError("Source upload was not confirmed. Check source encryption configuration and staff access, then refresh the inventory before retrying."); }
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
      setMessage(`Published snapshot ${result.publication_id}. Public caches refresh within one minute.`); setRetry((value) => value + 1); setConfirmed(false);
    } catch { setError("Publication was not confirmed. Check column headers, uploaded source IDs, dates, units, and illustrative flags; refresh history before retrying."); }
    finally { setBusy(false); }
  }
  return <section className="panel"><h1>Spreadsheet publication</h1><p><Link href="/staff">Staff back office</Link> · <Link href="/staff/exports">Exports</Link></p>
    <p>Edit approved content in the source spreadsheet, then import its CSV export. This publishes one complete snapshot of planned initiatives and the static ledger.</p>
    {data && <>
      <form onSubmit={upload}><fieldset disabled={busy}><legend>Retain a source document</legend><label>Source file<input type="file" onChange={(event) => setSource(event.target.files?.[0] || null)} required /></label><button>Upload private source</button></fieldset></form>
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
        <button>Confirm publication</button>
      </fieldset></form>
      <h2>Publication history</h2><ul>{data.publications.map((item) => <li key={item.id}>{new Date(item.at).toLocaleString()} · <code>{item.id}</code></li>)}</ul>
    </>}{message && <p role="status">{message}</p>}{error && <p role="alert">{error}</p>}
    <button disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh content inventory</button>
  </section>;
}

"use client";
import Link from "next/link";
import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { ApiError, api, type FieldError, failureMessage, type Schema } from "@/lib/api";
import { formatDateTime } from "@/lib/dates";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

export default function Content() {
  const { resource, data, reload } = useApiResource<Schema<"ContentInventory">>("/donor/staff/content");
  const [source, setSource] = useState<File | null>(null);
  const [ledger, setLedger] = useState<File | null>(null);
  const [asOf, setAsOf] = useState("");
  const [cadence, setCadence] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [rowErrors, setRowErrors] = useState<FieldError[]>([]);
  const [message, setMessage] = useState("");
  const messages = resourceMessages(resource, {
    loading: "Loading content inventory…",
    failure: "The content inventory could not be loaded right now. Please retry in a moment.",
    forbidden: "Content tools require administrator access.",
  });
  const rejected = (cause: unknown, fallback: string) =>
    cause instanceof ApiError && cause.status === 403
      ? "Your staff role does not permit publishing."
      : failureMessage(cause, fallback);
  async function upload(event: React.FormEvent) {
    event.preventDefault();
    if (!source || busy) return;
    if (source.size > 10 * 1024 * 1024) {
      setError("Source documents must be at most 10 MB.");
      return;
    }
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api<Schema<"StoredSource">>(
        `/donor/staff/content/sources?name=${encodeURIComponent(source.name)}`,
        { method: "POST", body: source },
      );
      setMessage(`Source stored: ${result.id}. Use this ID in the spreadsheet.`);
      reload();
    } catch (cause) {
      setError(
        rejected(
          cause,
          "Source upload was not confirmed. Check source encryption configuration, then refresh the inventory before retrying.",
        ),
      );
    } finally {
      setBusy(false);
    }
  }
  async function publish(event: React.FormEvent) {
    event.preventDefault();
    if (!ledger || !confirmed || busy) return;
    if (ledger.size > 512 * 1024) {
      setError("The CSV must be at most 512 KB.");
      return;
    }
    setBusy(true);
    setError("");
    setMessage("");
    setRowErrors([]);
    try {
      const result = await api<Schema<"Publication">>("/donor/staff/content/publish", {
        method: "POST",
        body: JSON.stringify({
          ledger_csv: await ledger.text(),
          as_of: asOf,
          cadence,
        } satisfies Schema<"PublicationRequest">),
      });
      setMessage(`Published snapshot ${result.publication_id}. Public caches refresh within one minute.`);
      reload();
      setConfirmed(false);
    } catch (cause) {
      if (cause instanceof ApiError) setRowErrors(cause.errors);
      setError(
        rejected(
          cause,
          "Publication was not confirmed. Check column headers, uploaded source IDs, units, and illustrative flags; refresh history before retrying.",
        ),
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <Page wide eyebrow="Staff" back={{ href: "/staff", label: "Staff back office" }} title="Spreadsheet publication">
      <p>
        <Link href="/staff/exports">Exports</Link> ·{" "}
        <Link href="/staff/content/wording">Badge and milestone wording</Link>
      </p>
      <p>
        Edit approved figures in the source spreadsheet, then import its CSV export. This publishes one complete
        snapshot of the impact ledger. Initiatives are edited in <Link href="/staff/initiatives">Initiatives</Link>.
      </p>
      {resource.state === "signedOut" && (
        <p>
          <Link className="button" href="/staff/sign-in">
            Staff sign-in
          </Link>
        </p>
      )}
      {data && (
        <>
          <form onSubmit={upload}>
            <fieldset disabled={busy}>
              <legend>Retain a source document</legend>
              <label>
                Source file
                <input type="file" onChange={(event) => setSource(event.target.files?.[0] || null)} required />
              </label>
              <button type="submit" className="secondary">
                Upload private source
              </button>
            </fieldset>
          </form>
          <h2>Source inventory</h2>
          {data.sources.length ? (
            <ul>
              {data.sources.map((item) => (
                <li key={item.id}>
                  <a href={`/api/donor/staff/content/sources/${item.id}`}>{item.name}</a> · ID <code>{item.id}</code> ·
                  SHA-256 <code>{item.sha256}</code>
                </li>
              ))}
            </ul>
          ) : (
            <p>No source documents stored.</p>
          )}
          <form onSubmit={publish}>
            <fieldset disabled={busy}>
              <legend>Publish approved spreadsheet snapshot</legend>
              <p>Ledger CSV columns:</p>
              <pre>metric,value,unit,source_id,illustrative</pre>
              <p>
                Units: USD_CENTS, PEOPLE, INITIATIVES, PARTNERS. Values are nonnegative integers. Illustrative must be
                true or false; real figures require a stored source ID. Empty sheets must retain their column headers.
              </p>
              <label>
                Ledger CSV
                <input
                  type="file"
                  accept=".csv,text/csv"
                  onChange={(event) => setLedger(event.target.files?.[0] || null)}
                  required
                />
              </label>
              <label>
                Ledger as-of date
                <input type="date" value={asOf} onChange={(event) => setAsOf(event.target.value)} required />
              </label>
              <label>
                Update cadence
                <input
                  maxLength={100}
                  placeholder="For example: monthly"
                  value={cadence}
                  onChange={(event) => setCadence(event.target.value)}
                  required
                />
              </label>
              <label className="choice">
                <input
                  type="checkbox"
                  checked={confirmed}
                  onChange={(event) => setConfirmed(event.target.checked)}
                  required
                />{" "}
                I checked source documents, illustrative labels, and the as-of date. Publish this snapshot publicly.
              </label>
              <button type="submit" className="button">
                Confirm publication
              </button>
            </fieldset>
          </form>
          <h2>Publication history</h2>
          <ul>
            {data.publications.map((item) => (
              <li key={item.id}>
                {formatDateTime(item.at)} · <code>{item.id}</code>
              </li>
            ))}
          </ul>
        </>
      )}
      <Announcer status={messages.status || message} alert={error || messages.alert} />
      {rowErrors.length > 0 && (
        <ul className="rows" aria-label="Spreadsheet problems">
          {rowErrors.map((item) => (
            <li key={`${item.sheet}:${item.row}:${item.column}`}>
              <span>
                {item.sheet && `${item.sheet} sheet, `}Row {item.row}, column <code>{item.column}</code>
              </span>
              <span>{item.reason}</span>
            </li>
          ))}
        </ul>
      )}
      {resource.state !== "signedOut" && (
        <button type="button" className="secondary" disabled={busy} onClick={reload}>
          Refresh content inventory
        </button>
      )}
    </Page>
  );
}

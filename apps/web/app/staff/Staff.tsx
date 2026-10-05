"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, failureMessage } from "@/lib/api";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

export type StaffMember = { id: string; email: string; name: string; role: string; permissions: string[]; active: boolean; disabled: boolean };
type Donors = { donors: { id: string; email: string; public_name: string | null; claimed: boolean; disabled: boolean }[]; next_offset: number | null };
type Detail = { id: string; email: string; public_name: string | null;
  streak: { current: number | null; best: number; at_risk: boolean; computed_at: string } | null;
  referrer: { id: string; email: string; type: string } | null;
  gifts: { id: string; amount_minor: number; currency: string; frequency: string; paid_at: string }[];
  referred: { id: string; email: string; type: string; net_minor: number | null }[];
  next_gifts_offset: number | null; next_referrals_offset: number | null };

export default function Staff() {
  const [signedOut, setSignedOut] = useState(false);
  const { resource, data: actor, reload } = useApiResource<StaffMember>(signedOut ? null : "/donor/staff/me");
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [results, setResults] = useState<Donors | null>(null);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [accounts, setAccounts] = useState<StaffMember[] | null>(null);
  const [invitation, setInvitation] = useState("");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("SUPPORT");
  const [busy, setBusy] = useState(false);
  const messages = resourceMessages(resource, { loading: "Checking staff access…",
    failure: "Staff access could not be loaded right now. Please retry in a moment.",
    forbidden: "This account does not have staff access." });
  async function action(work: () => Promise<void>) {
    if (busy) return;
    setBusy(true); setError("");
    try { await work(); }
    catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else if (cause instanceof ApiError && cause.status === 403) setError("Your staff role does not permit this action.");
      else setError(failureMessage(cause, "The request was not confirmed. Refresh to check its status before retrying."));
    } finally { setBusy(false); }
  }
  const search = (offset = 0) => action(async () => { setResults(await api<Donors>(`/donor/staff/donors?q=${encodeURIComponent(q)}&offset=${offset}`)); setDetail(null); });
  const open = (id: string, gifts = 0, referrals = 0) => action(async () => {
    setDetail(await api<Detail>(`/donor/staff/donors/${encodeURIComponent(id)}?gifts_offset=${gifts}&referrals_offset=${referrals}`));
  });
  const refreshAccounts = async () => setAccounts(await api<StaffMember[]>("/donor/staff/accounts"));
  const absent = signedOut || resource.state === "signedOut";
  return <section className="panel"><h1>DAD staff back office</h1><p>Sandbox donor records. Staff access and actions are audited.</p>
    {absent ? <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p> : actor && <>
      <p>{actor.name} · {actor.role}</p>
      <nav aria-label="Staff tools" className="accountLinks">
        {actor.permissions.includes("data.export") && <Link href="/staff/exports">Exports and enrolment queue</Link>}
        {actor.permissions.includes("content.publish") && <><Link href="/staff/content">Publish spreadsheet content</Link><Link href="/staff/partners">Partner inquiries</Link></>}
        {actor.permissions.includes("audit.read") && <Link href="/staff/audit">Audit history</Link>}
      </nav>
      <form onSubmit={(event) => { event.preventDefault(); search(); }}>
        <label>Find donor by email<input type="search" maxLength={320} value={q} onChange={(event) => setQ(event.target.value)} /></label>
        <button type="submit" className="button" disabled={busy}>Search donors</button>
      </form>
      {results && <><h2>Donors</h2>{results.donors.length ? <ul>{results.donors.map((donor) => <li key={donor.id}>
        <button type="button" className="quiet" disabled={busy} onClick={() => open(donor.id)}>{donor.email}</button> · {donor.claimed ? "Claimed" : "Guest"}{donor.disabled ? " · Disabled" : ""}
      </li>)}</ul> : <p>No matching donors.</p>}
        {results.next_offset !== null && <button type="button" className="secondary" disabled={busy} onClick={() => search(results.next_offset ?? 0)}>Next donor page</button>}
      </>}
      {detail && <article><h2>{detail.email}</h2><p><Link href={`/staff/messages?donor=${encodeURIComponent(detail.id)}`}>Delivery history and resends</Link></p><p>Public nickname: {detail.public_name || "Not shared"}</p>
        <p>Streak: {detail.streak?.current ?? "Updating"} · Best: {detail.streak?.best ?? "Updating"}{detail.streak?.at_risk ? " · At risk" : ""}</p>
        {detail.streak && <p>Computed {new Date(detail.streak.computed_at).toLocaleString()}</p>}
        <p>Referrer: {detail.referrer ? `${detail.referrer.email} · ${detail.referrer.type}` : "No attributed referrer"}</p>
        <h3>Gifts</h3>{actor.permissions.includes("gift.cancel") && <p><Link href={`/staff/finance?donor=${encodeURIComponent(detail.id)}`}>Cancel monthly giving</Link></p>}{detail.gifts.length ? <ul>{detail.gifts.map((gift) => <li key={gift.id}>
          {formatMoney(gift.amount_minor, gift.currency)} · {gift.frequency} · {new Date(gift.paid_at).toLocaleString()} · <code>{gift.id}</code>{actor.permissions.includes("gift.refund") && <> · <Link href={`/staff/finance?donor=${encodeURIComponent(detail.id)}&gift=${encodeURIComponent(gift.id)}`}>Refund gift</Link></>}
        </li>)}</ul> : <p>No gifts recorded.</p>}
        {detail.next_gifts_offset !== null && <button type="button" className="secondary" disabled={busy} onClick={() => open(detail.id, detail.next_gifts_offset ?? 0)}>Older gifts</button>}
        <h3>People referred</h3>{detail.referred.length ? <ul>{detail.referred.map((person) => <li key={person.id}>
          {person.email} · {person.type} · Net {person.net_minor === null ? "Updating" : formatMoney(person.net_minor)}
        </li>)}</ul> : <p>No converting referrals recorded.</p>}
        {detail.next_referrals_offset !== null && <button type="button" className="secondary" disabled={busy} onClick={() => open(detail.id, 0, detail.next_referrals_offset ?? 0)}>More referrals</button>}
      </article>}
      {actor.permissions.includes("staff.manage") && <section><h2>Staff access</h2>
        <button type="button" className="secondary" disabled={busy} onClick={() => action(refreshAccounts)}>Refresh staff accounts</button>
        {accounts && <ul>{accounts.map((account) => <li key={account.id}>{account.name} · {account.email} · {account.role} · {account.disabled ? "Disabled" : account.active ? "Active" : "Invited"}
          {account.id !== actor.id && <>
            <details><summary>Replace credentials or invitation</summary>
              <p>This revokes existing sessions and invitations. The staff member must choose a new password and authenticator. Disabled accounts are restored only after completing setup.</p>
              <button type="button" className="secondary danger" disabled={busy} onClick={() => action(async () => { const result = await api<{ invitation_url: string }>(`/donor/staff/accounts/${account.id}/recovery`, { method: "POST" }); setInvitation(result.invitation_url); await refreshAccounts(); })}>Confirm replacement invitation</button>
            </details>
            <details><summary>Change role</summary>
              <form onSubmit={(event) => { event.preventDefault(); const chosen = new FormData(event.currentTarget).get("role"); action(async () => { await api(`/donor/staff/accounts/${account.id}/role`, { method: "POST", body: JSON.stringify({ role: chosen }) }); await refreshAccounts(); }); }}>
                <label>New role for {account.name}<select name="role" defaultValue={account.role}><option>SUPPORT</option><option>FINANCE</option><option>ADMIN</option></select></label>
                <p>A role change ends existing sessions and requires a new sign-in.</p><button type="submit" className="secondary" disabled={busy}>Confirm role change</button>
              </form>
            </details>
          </>}
          {!account.disabled && account.id !== actor.id && <details><summary>Disable account</summary><p>This immediately revokes staff access.</p>
            <button type="button" className="secondary danger" disabled={busy} onClick={() => action(async () => { await api(`/donor/staff/accounts/${account.id}/disable`, { method: "POST" }); await refreshAccounts(); })}>Confirm disable {account.name}</button>
          </details>}
        </li>)}</ul>}
        <form onSubmit={(event) => { event.preventDefault(); action(async () => { const result = await api<{ invitation_url: string }>("/donor/staff/accounts", { method: "POST", body: JSON.stringify({ email, name, role }) }); setInvitation(result.invitation_url); }); }}>
          <fieldset disabled={busy}><legend>Invite individual staff member</legend>
            <label>Name<input value={name} maxLength={100} onChange={(event) => setName(event.target.value)} required /></label>
            <label>Email<input type="email" value={email} maxLength={320} onChange={(event) => setEmail(event.target.value)} required /></label>
            <label>Role<select value={role} onChange={(event) => setRole(event.target.value)}><option>SUPPORT</option><option>FINANCE</option><option>ADMIN</option></select></label>
            <button type="submit" className="button">Create private invitation</button>
          </fieldset>
        </form>
        {invitation && <p>Share this one-use, 24-hour link through your approved private channel: <code>{invitation}</code></p>}
      </section>}
      <div className="actionRow">
        <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh access</button>
        <button type="button" className="quiet" disabled={busy} onClick={() => action(async () => { await api("/donor/staff/sign-out", { method: "POST" }); setSignedOut(true); })}>Sign out</button>
      </div>
    </>}
    {(resource.state === "offline" || resource.state === "error") && <button type="button" className="secondary" onClick={reload}>Refresh access</button>}
    <Announcer status={absent ? "" : busy ? "Working…" : invitation ? "Invitation created." : messages.status} alert={absent ? "" : error || messages.alert} />
  </section>;
}

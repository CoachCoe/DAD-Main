"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Profile = { email: string; public_name: string | null; version: number; referral_emails: boolean; marketing_emails: boolean };
export default function ProfilePage() {
  const [data, setData] = useState<Profile | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const [newEmail, setNewEmail] = useState("");
  useEffect(() => {
    let active = true;
    setData(null); setError(""); setMessage("");
    api<Profile>("/donor/me/profile", { cache: retry > 0 ? "reload" : "default" }).then((result) => { if (active) setData(result); }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Could not load your profile. Check your connection and retry.");
    });
    return () => { active = false; };
  }, [retry]);
  async function save(event: React.FormEvent) {
    event.preventDefault(); if (!data || busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      setData(await api<Profile>("/donor/me/preferences", { method: "POST", body: JSON.stringify({ version: data.version,
        referral_emails: data.referral_emails, marketing_emails: data.marketing_emails }) }));
      setMessage("Notification preferences saved.");
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Preferences were not confirmed. Refresh to check the saved choices before retrying.");
    } finally { setBusy(false); }
  }
  async function logoutAll() {
    if (busy) return;
    setBusy(true); setError("");
    try { await api("/donor/logout-all", { method: "POST" }); setSignedOut(true); setData(null); }
    catch { setError("Sign-out was not confirmed. Retry when connected."); }
    finally { setBusy(false); }
  }
  async function changeEmail(event: React.FormEvent) {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      await api("/donor/me/email-change", { method: "POST", body: JSON.stringify({email: newEmail}) });
      setMessage("Check your current email address first. After you confirm there, we’ll send a second confirmation to your new address. Each link expires after 15 minutes.");
      setNewEmail("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Could not request the change. Please retry."); }
    finally { setBusy(false); }
  }
  return <section className="panel">
    <h1>Profile and notification preferences</h1>
    <p><Link href="/account/dashboard">Donor dashboard</Link></p>
    {signedOut ? <Link href="/account/sign-in">Sign in to manage your profile</Link> : <>
      {!data && !error && <p role="status">Loading profile…</p>}
      {data && <>
        <p>Account email: {data.email}</p>
        <details><summary>Change account email</summary>
          <p>You must confirm both addresses. Completing the change signs out every device. Existing accounts will not be merged.</p>
          <form onSubmit={changeEmail}><label>New email address<input type="email" autoComplete="email" maxLength={320} required value={newEmail} onChange={(event) => setNewEmail(event.target.value)} disabled={busy} /></label>
            <button type="submit" disabled={busy}>Send confirmation to current address</button></form>
        </details>
        <p>Public nickname: {data.public_name || "Not shared"}. <Link href="/account/share">Manage sharing and public nickname</Link></p>
        <form onSubmit={save}>
          <fieldset disabled={busy}><legend>Optional email notifications</legend>
            <label><input type="checkbox" checked={data.referral_emails} onChange={(event) => setData({ ...data, referral_emails: event.target.checked })} /> Tell me when a Link or IRL Referral gives for the first time</label>
            <label><input type="checkbox" checked={data.marketing_emails} onChange={(event) => setData({ ...data, marketing_emails: event.target.checked })} /> Send me DAD news and campaign updates</label>
          </fieldset>
          <p>These choices are optional and start off. Gift records, account access, and essential payment notices are separate.</p>
          <button disabled={busy} type="submit">{busy ? "Saving…" : "Save preferences"}</button>
        </form>
        <details><summary>Sign out on every device</summary>
          <p>This ends all existing donor sessions, including this one. You can sign in again with a new email link.</p>
          <button type="button" disabled={busy} onClick={logoutAll}>Confirm sign out everywhere</button>
        </details>
      </>}
      <button type="button" disabled={busy} onClick={() => setRetry((value) => value + 1)}>Refresh profile</button>
    </>}
    {message && !signedOut && <p role="status">{message}</p>}
    {error && <p role="alert">{error}</p>}
  </section>;
}

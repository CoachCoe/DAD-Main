"use client";

import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api, ApiError, failureMessage } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Profile = { email: string; public_name: string | null; version: number; referral_emails: boolean; marketing_emails: boolean };

export default function Profile() {
  const [signedOut, setSignedOut] = useState(false);
  const { resource, data, reload, setData } = useApiResource<Profile>(signedOut ? null : "/donor/me/profile");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [newEmail, setNewEmail] = useState("");
  const messages = resourceMessages(resource, { loading: "Loading profile…",
    failure: "Your profile could not be loaded right now. Please retry in a moment." });
  async function run(work: () => Promise<void>, fallback: string) {
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try { await work(); }
    catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(failureMessage(cause, fallback));
    } finally { setBusy(false); }
  }
  const save = (event: React.FormEvent) => {
    event.preventDefault();
    if (!data) return;
    run(async () => {
      setData(await api<Profile>("/donor/me/preferences", { method: "POST", body: JSON.stringify({ version: data.version,
        referral_emails: data.referral_emails, marketing_emails: data.marketing_emails }) }));
      setMessage("Notification preferences saved.");
    }, "Preferences were not confirmed. Refresh to check the saved choices before retrying.");
  };
  const changeEmail = (event: React.FormEvent) => {
    event.preventDefault();
    run(async () => {
      await api("/donor/me/email-change", { method: "POST", body: JSON.stringify({ email: newEmail }) });
      setMessage("Check your current email address first. After you confirm there, we’ll send a second confirmation to your new address. Each link expires after 15 minutes.");
      setNewEmail("");
    }, "We couldn’t request the change right now. Please retry in a moment.");
  };
  const logoutAll = () => run(async () => {
    await api("/donor/logout-all", { method: "POST" });
    setSignedOut(true);
  }, "Sign-out was not confirmed. Please retry.");
  const absent = signedOut || resource.state === "signedOut";
  return <section className="panel">
    <h1>Profile and notification preferences</h1>
    <p><Link href="/account/dashboard">Donor dashboard</Link></p>
    {absent ? <p><Link href="/account/sign-in">Sign in to manage your profile</Link></p> : <>
      {data && <>
        <p>Account email: {data.email}</p>
        <details><summary>Change account email</summary>
          <p>You must confirm both addresses. Completing the change signs out every device. Existing accounts will not be merged.</p>
          <form onSubmit={changeEmail}><label>New email address<input type="email" autoComplete="email" maxLength={320} required value={newEmail} onChange={(event) => setNewEmail(event.target.value)} disabled={busy} /></label>
            <button type="submit" className="secondary" disabled={busy}>Send confirmation to current address</button></form>
        </details>
        <p>Public nickname: {data.public_name || "Not shared"}. <Link href="/account/share">Manage sharing and public nickname</Link></p>
        <form onSubmit={save}>
          <fieldset disabled={busy}><legend>Optional email notifications</legend>
            <label><input type="checkbox" checked={data.referral_emails} onChange={(event) => setData({ ...data, referral_emails: event.target.checked })} /> Tell me when a Link or IRL Referral gives for the first time</label>
            <label><input type="checkbox" checked={data.marketing_emails} onChange={(event) => setData({ ...data, marketing_emails: event.target.checked })} /> Send me DAD news and campaign updates</label>
          </fieldset>
          <p>These choices are optional and start off. Gift records, account access, and essential payment notices are separate.</p>
          <button className="button" disabled={busy} type="submit">{busy ? "Saving…" : "Save preferences"}</button>
        </form>
        <details><summary>Sign out on every device</summary>
          <p>This ends all existing donor sessions, including this one. You can sign in again with a new email link.</p>
          <button type="button" className="secondary danger" disabled={busy} onClick={logoutAll}>Confirm sign out everywhere</button>
        </details>
      </>}
      <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh profile</button>
    </>}
    <Announcer status={absent ? "" : messages.status || message} alert={absent ? "" : error || messages.alert} />
  </section>;
}

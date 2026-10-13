"use client";

import { useCallback, useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { api, ApiError, failureMessage, type RequestBody, type Schema } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { Passkeys } from "./Passkeys";
import { Sessions } from "./Sessions";

export default function Profile() {
  const [signedOut, setSignedOut] = useState(false);
  const { resource, data, failed, reload, setData } = useApiResource<Schema<"Profile">>(signedOut ? null : "/donor/me/profile");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [newEmail, setNewEmail] = useState("");
  const markSignedOut = useCallback(() => setSignedOut(true), []);
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
      setData(await api<Schema<"Profile">>("/donor/me/preferences", { method: "POST", body: JSON.stringify({ version: data.version,
        referral_emails: data.referral_emails, marketing_emails: data.marketing_emails, push_referral_converted: data.push_referral_converted,
        push_streak_at_risk: data.push_streak_at_risk, push_streak_milestone: data.push_streak_milestone } satisfies RequestBody<"/donor/me/preferences", "post">) }));
      setMessage("Notification preferences saved.");
    }, "Preferences were not confirmed. Refresh to check the saved choices before retrying.");
  };
  const changeEmail = (event: React.FormEvent) => {
    event.preventDefault();
    run(async () => {
      await api("/donor/me/email-change", { method: "POST", body: JSON.stringify({ email: newEmail } satisfies RequestBody<"/donor/me/email-change", "post">) });
      setMessage("Check your current email address first. After you confirm there, we’ll send a second confirmation to your new address. Each link expires after 15 minutes.");
      setNewEmail("");
    }, "We couldn’t request the change right now. Please retry in a moment.");
  };
  const logout = () => run(async () => {
    await api("/donor/logout", { method: "POST" });
    setSignedOut(true);
  }, "Sign-out was not confirmed. Please retry.");
  const logoutAll = () => run(async () => {
    await api("/donor/logout-all", { method: "POST" });
    setSignedOut(true);
  }, "Sign-out was not confirmed. Please retry.");
  return <Page eyebrow="My account" title="Profile and notifications" back={{ href: "/account/dashboard", label: "My dashboard" }}>
    {signedOut || resource.state === "signedOut" ? <SignInPrompt>Sign in to manage your profile</SignInPrompt> : <>
      {data && <>
        <ul className="rows">
          <li><span>Account email</span><strong>{data.email}</strong></li>
          <li><span>Public nickname</span><span><strong>{data.public_name || "Not shared"}</strong> · <Link href="/account/share">Manage</Link></span></li>
        </ul>
        <form onSubmit={save}>
          <fieldset disabled={busy}><legend>Optional email notifications</legend>
            <label className="choice"><input type="checkbox" checked={data.referral_emails} onChange={(event) => setData({ ...data, referral_emails: event.target.checked })} /> Tell me when a Link or IRL Referral gives for the first time</label>
            <label className="choice"><input type="checkbox" checked={data.marketing_emails} onChange={(event) => setData({ ...data, marketing_emails: event.target.checked })} /> Send me DAD news and campaign updates</label>
          </fieldset>
          <p className="note">These choices are optional and start off. Gift records, account access, and essential payment notices are separate.</p>
          <fieldset disabled={busy}><legend>DAD app notifications</legend>
            <label className="choice"><input type="checkbox" checked={data.push_referral_converted} onChange={(event) => setData({ ...data, push_referral_converted: event.target.checked })} /> When someone I invited gives for the first time</label>
            <label className="choice"><input type="checkbox" checked={data.push_streak_at_risk} onChange={(event) => setData({ ...data, push_streak_at_risk: event.target.checked })} /> When a declined payment puts my giving streak at risk</label>
            <label className="choice"><input type="checkbox" checked={data.push_streak_milestone} onChange={(event) => setData({ ...data, push_streak_milestone: event.target.checked })} /> When my giving streak reaches a milestone</label>
          </fieldset>
          <p className="note">App notifications reach only phones where you signed in to the DAD app and allowed its notifications. They start on and never show names or amounts.</p>
          <button className="button" disabled={busy} type="submit">{busy ? "Saving…" : "Save preferences"}</button>
        </form>
        <details><summary>Change account email</summary>
          <p>You must confirm both addresses. Completing the change signs out every device and removes your passkeys. Existing accounts will not be merged.</p>
          <form onSubmit={changeEmail}><label>New email address<input type="email" autoComplete="email" maxLength={320} required value={newEmail} onChange={(event) => setNewEmail(event.target.value)} disabled={busy} /></label>
            <button type="submit" className="secondary" disabled={busy}>Send confirmation to current address</button></form>
        </details>
        <Passkeys onSignedOut={markSignedOut} />
        <Sessions onSignedOut={markSignedOut} />
        <button type="button" className="secondary" disabled={busy} onClick={logout}>Sign out of this device</button>
        <details><summary>Sign out on every device</summary>
          <p>This ends all existing donor sessions, including this one. You can sign in again with a new email link. Your passkeys are kept, so remove any you don’t recognize under Passkeys first.</p>
          <button type="button" className="secondary danger" disabled={busy} onClick={logoutAll}>Confirm sign out everywhere</button>
        </details>
      </>}
      <Announcer status={messages.status || message} alert={error || messages.alert} />
      {(failed || error) && <button type="button" className="secondary" disabled={busy} onClick={reload}>Refresh profile</button>}
    </>}
  </Page>;
}

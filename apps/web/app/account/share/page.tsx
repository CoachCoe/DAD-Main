"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

type Sharing = { public_name: string | null; link: string | null; irl_link: string | null };

export default function SharePage() {
  const [sharing, setSharing] = useState<Sharing | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [signedOut, setSignedOut] = useState(false);
  const [replaceCode, setReplaceCode] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setError("");
    api<Sharing>("/donor/me/sharing").then((result) => {
      if (active) { setSharing(result); setName(result.public_name ?? ""); }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Sharing could not be loaded. Please retry.");
    });
    return () => { active = false; };
  }, [retry]);
  async function save(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      setSharing(await api<Sharing>("/donor/me/sharing", { method: "POST",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify({ public_name: name, replace_code: replaceCode }) }));
      setReplaceCode(false); setMessage("Your sharing details are saved.");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Could not save. Please retry."); }
    finally { setBusy(false); }
  }
  const text = "Join me in DAD’s sandbox preview. DAD launches in April 2027. About a dollar a day means $30/month. No real donation is collected in this preview.";
  async function share() {
    if (!sharing?.link) return;
    setError(""); setMessage("");
    try {
      if (typeof navigator.share === "function") {
        await navigator.share({ title: "Join me at DAD", text, url: sharing.link });
      } else {
        await navigator.clipboard.writeText(`${text}\n${sharing.link}`);
        setMessage("Invitation copied. Paste it into a message to someone you know.");
      }
    } catch (cause) {
      if (!(cause instanceof Error && cause.name === "AbortError")) {
        setError("Sharing isn’t available here. You can select and copy the invitation below.");
      }
    }
  }
  return <section className="panel authPanel">
    <span className="eyebrow">DAD · SANDBOX PREVIEW</span><h1>Invite someone you know</h1>
    {signedOut ? <Link href="/account/sign-in">Sign in to share</Link> : <>
      {!sharing && !error && <p role="status">Loading your sharing details…</p>}
      {sharing && <>
        <form onSubmit={save}>
          <label className="field">Public first name or nickname
            <input required maxLength={80} value={name} onChange={(event) => setName(event.target.value)} /></label>
          <p>This name appears to anyone who opens your referral link or scans your code. Choose a name you’re comfortable sharing publicly.</p>
          {replaceCode && <p role="alert">Replacing your link also changes your on-screen code. Previously shared links stop working for new visitors.</p>}
          <button disabled={busy}>{busy ? "Saving…" : replaceCode ? "Confirm replacement" : "Save public name"}</button>
          {replaceCode && <button type="button" onClick={() => setReplaceCode(false)}>Keep current link</button>}
        </form>
        {sharing.link && <>
          <label className="field">Your referral link<input readOnly value={sharing.link} onFocus={(event) => event.target.select()} /></label>
          <label className="field">Invitation<textarea readOnly rows={5} value={`${text}\n${sharing.link}`} /></label>
          <p><button onClick={share}>Share invitation</button></p>
          <p><Link href="/account/code">Show my referral code in person</Link></p>
          <button onClick={() => setReplaceCode(true)}>Replace my link and code</button>
        </>}
      </>}
      {error && <><p role="alert">{error}</p><button onClick={() => setRetry((value) => value + 1)}>Retry loading</button></>}
      {message && <p role="status">{message}</p>}
      <p><Link href="/account/dashboard">My dashboard</Link></p>
    </>}
  </section>;
}

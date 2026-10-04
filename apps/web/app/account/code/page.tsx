"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import Image from "next/image";
import { api, ApiError } from "@/lib/api";

export default function CodePage() {
  const [full, setFull] = useState(false);
  const [ready, setReady] = useState(false);
  const [needsName, setNeedsName] = useState(false);
  const [signedOut, setSignedOut] = useState(false);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    let active = true;
    setError(""); setReady(false);
    api<{ irl_link: string | null }>("/donor/me/sharing", { cache: retry > 0 ? "reload" : "default" }).then((result) => {
      if (active) { setReady(Boolean(result.irl_link)); setNeedsName(!result.irl_link); }
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError("Your referral code could not be loaded. Please retry.");
    });
    return () => { active = false; };
  }, [retry]);
  useEffect(() => {
    const update = () => setFull(document.fullscreenElement === panel.current);
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, []);
  async function fullscreen() {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await panel.current?.requestFullscreen();
    }
    catch { setError("Full-screen mode isn’t available. You can still scan the code on this page."); }
  }
  return <section className="panel donorCodePanel" ref={panel}>
    <span className="eyebrow">DAD · SANDBOX PREVIEW</span><h1>My referral code</h1>
    {signedOut ? <Link href="/account/sign-in">Sign in to show your code</Link> : needsName ?
      <Link href="/account/share">Choose your public nickname to enable sharing</Link> : <>
        {!ready && !error && <p role="status">Loading your code…</p>}
        {ready && <>
          <p>Ask someone to scan this with their phone’s camera. They don’t need an account or an app.</p>
          <Image unoptimized className="donorReferralCode" src={`/api/donor/me/referral-code.svg?view=${retry}`}
            alt="Your scannable in-person DAD referral code" width={360} height={360}
            onError={() => { setReady(false); setError("The code image could not be loaded. Please retry."); }} />
          <p>Increase your screen brightness and keep the whole code visible.</p>
          <button type="button" onClick={fullscreen}>{full ? "Exit full screen" : "Show full screen"}</button>
          <p>About a dollar a day · $30/month. DAD launches in April 2027.</p>
          <p>This preview collects no real donations.</p>
        </>}
      </>}
    {error && <><p role="alert">{error}</p><button type="button" onClick={() => setRetry((value) => value + 1)}>Retry code</button></>}
    <p><Link href="/account/share">Share by link</Link> · <Link href="/account/dashboard">My dashboard</Link></p>
  </section>;
}

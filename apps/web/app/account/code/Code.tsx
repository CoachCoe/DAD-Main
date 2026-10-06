"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import WalletButtons from "./WalletButtons";
import { Announcer } from "@/components/donor/Announcer";
import { GiftFraming } from "@/components/donor/GiftFraming";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

/** Keeps the screen on while the code is shown. Browsers drop the lock when the page is hidden. */
function useScreenWakeLock(active: boolean) {
  useEffect(() => {
    if (!active || !("wakeLock" in navigator)) return;
    let lock: WakeLockSentinel | null = null;
    let released = false;
    const acquire = () => {
      if (document.visibilityState !== "visible") return;
      navigator.wakeLock.request("screen").then((sentinel) => {
        if (released) sentinel.release().catch(() => undefined);
        else lock = sentinel;
      }).catch(() => undefined);
    };
    acquire();
    document.addEventListener("visibilitychange", acquire);
    return () => {
      released = true;
      document.removeEventListener("visibilitychange", acquire);
      lock?.release().catch(() => undefined);
    };
  }, [active]);
}

export default function Code() {
  const { resource, data, failed, reload } = useApiResource<{ irl_link: string | null }>("/donor/me/sharing");
  const [full, setFull] = useState(false);
  const [imageFailed, setImageFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [fullscreenError, setFullscreenError] = useState("");
  const panel = useRef<HTMLElement>(null);
  const ready = Boolean(data?.irl_link) && !imageFailed;
  useScreenWakeLock(ready);
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
    catch { setFullscreenError("Full-screen mode isn’t available. You can still scan the code on this page."); }
  }
  function retry() {
    setImageFailed(false);
    setAttempt((value) => value + 1);
    reload();
  }
  const messages = resourceMessages(resource, { loading: "Loading your code…",
    failure: "Your referral code could not be loaded right now. Please retry in a moment." });
  const alert = imageFailed ? "The code image could not be loaded. Please retry." : fullscreenError || messages.alert;
  return <Page ref={panel} className="codePanel" eyebrow="Show in person" title="My referral code"
    back={{ href: "/account/dashboard", label: "My dashboard" }}>
    {resource.state === "signedOut" ? <SignInPrompt>Sign in to show your code</SignInPrompt>
      : data && !data.irl_link ? <div className="callout">
        <p>Your code appears once you choose the public nickname people will see.</p>
        <Link className="button" href="/account/share">Choose your public nickname to enable sharing</Link>
      </div> : <>
        {ready && <>
          <p>Ask someone to scan this with their phone’s camera. They don’t need an account or an app.</p>
          {/* biome-ignore lint/performance/noImgElement: a per-donor, same-origin SVG that the image optimizer would only copy */}
          <img className="referralCode" src={`/api/donor/me/referral-code.svg?view=${attempt}`}
            alt="Your scannable in-person DAD referral code" width={360} height={360} onError={() => setImageFailed(true)} />
          <p><GiftFraming minor={3000} /></p>
          <div className="actions">
            <button type="button" className="button" onClick={fullscreen}>{full ? "Exit full screen" : "Show full screen"}</button>
          </div>
          <p className="note">Increase your screen brightness and keep the whole code visible. This page keeps your screen on while the code is shown. DAD launches in April 2027; this preview collects no real donations.</p>
          <WalletButtons />
        </>}
        {(imageFailed || failed) && <button type="button" className="secondary" onClick={retry}>Retry code</button>}
      </>}
    <Announcer status={messages.status} alert={alert} />
    <p><Link href="/account/share">Share by link instead</Link></p>
  </Page>;
}

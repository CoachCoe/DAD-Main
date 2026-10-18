"use client";

import Link from "next/link";
import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { ApiError, api, failureMessage, type RequestBody, type Schema } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { useStepFocus } from "@/lib/useStepFocus";

const text =
  "Join me in DAD’s sandbox preview. DAD launches in April 2027. About a dollar a day means $30/month. No real donation is collected in this preview.";

export default function Share() {
  const [name, setName] = useState("");
  const [signedOut, setSignedOut] = useState(false);
  const {
    resource,
    data: sharing,
    failed,
    reload,
    setData,
  } = useApiResource<Schema<"Sharing">>(signedOut ? null : "/donor/me/sharing", {
    onLoad: (result) => setName(result.public_name ?? ""),
  });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [replacing, setReplacing] = useState(false);
  const step = useStepFocus(replacing);
  const messages = resourceMessages(resource, {
    loading: "Loading your sharing details…",
    failure: "Sharing could not be loaded right now. Please retry in a moment.",
  });

  async function save(replace: boolean) {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      setData(
        await api<Schema<"Sharing">>("/donor/me/sharing", {
          method: "POST",
          body: JSON.stringify({ public_name: name, replace_code: replace } satisfies RequestBody<
            "/donor/me/sharing",
            "post"
          >),
        }),
      );
      setReplacing(false);
      setMessage("Your sharing details are saved.");
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else setError(failureMessage(cause, "Your sharing details were not saved. Please retry."));
    } finally {
      setBusy(false);
    }
  }
  async function share() {
    if (!sharing?.link) return;
    setError("");
    setMessage("");
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
  const absent = signedOut || resource.state === "signedOut";
  return (
    <Page eyebrow="Share" title="Invite someone you know" back={{ href: "/account/dashboard", label: "My dashboard" }}>
      {absent ? (
        <SignInPrompt>Sign in to share</SignInPrompt>
      ) : (
        <>
          {sharing && (
            <>
              {sharing.link ? (
                <>
                  <p className="lead">
                    Send your personal link. When someone gives for the first time through it, they count toward your
                    circle.
                  </p>
                  <button type="button" className="button full" onClick={share}>
                    Share invitation
                  </button>
                  <label>
                    Invitation
                    <textarea readOnly rows={5} value={`${text}\n${sharing.link}`} />
                  </label>
                  <label>
                    Your referral link
                    <input readOnly value={sharing.link} onFocus={(event) => event.target.select()} />
                  </label>
                  <p>
                    <Link href="/account/code">Show my referral code in person</Link>
                  </p>
                </>
              ) : (
                <p className="lead">
                  Choose the name people will see when they open your invitation. Then your personal link is ready to
                  share.
                </p>
              )}
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  save(false);
                }}
              >
                <label>
                  Public first name or nickname
                  <input
                    required
                    maxLength={80}
                    value={name}
                    disabled={busy || replacing}
                    aria-describedby="public-name-hint"
                    onChange={(event) => setName(event.target.value)}
                  />
                </label>
                <p id="public-name-hint" className="note">
                  This name appears to anyone who opens your referral link or scans your code. Choose a name you’re
                  comfortable sharing publicly.
                </p>
                <button type="submit" className={sharing.link ? "secondary" : "button"} disabled={busy || replacing}>
                  {busy && !replacing ? "Saving…" : "Save public name"}
                </button>
              </form>
              {sharing.link &&
                (replacing ? (
                  <div className="callout">
                    <h2 ref={step.heading} tabIndex={-1}>
                      Replace your link and code?
                    </h2>
                    <p>
                      Replacing your link also changes your on-screen code. Previously shared links stop working for new
                      visitors.
                    </p>
                    <div className="actions">
                      <button type="button" className="secondary danger" disabled={busy} onClick={() => save(true)}>
                        {busy ? "Replacing…" : "Confirm replacement"}
                      </button>
                      <button type="button" className="secondary" disabled={busy} onClick={() => setReplacing(false)}>
                        Keep current link
                      </button>
                    </div>
                  </div>
                ) : (
                  <button type="button" ref={step.trigger} className="quiet" onClick={() => setReplacing(true)}>
                    Replace my link and code
                  </button>
                ))}
            </>
          )}
          <Announcer status={messages.status || message} alert={error || messages.alert} />
          {failed && (
            <button type="button" className="secondary" onClick={reload}>
              Retry loading
            </button>
          )}
        </>
      )}
    </Page>
  );
}

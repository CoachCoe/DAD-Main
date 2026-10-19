"use client";
import Link from "next/link";
import { useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { ApiError, api, failureMessage, unconfirmedMessage } from "@/lib/api";
import { useFocusOnSuccess } from "@/lib/useStepFocus";

const initial = { organization: "", name: "", email: "", website: "", message: "", contact_consent: false };

export default function Partners() {
  const [data, setData] = useState(initial);
  const [frozen, setFrozen] = useState<typeof initial | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const heading = useFocusOnSuccess(done);
  const [error, setError] = useState("");
  const key = useRef<string | null>(null);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    // People type "example.org"; the API stores an HTTPS address.
    const website =
      data.website.trim() && !/^https?:\/\//i.test(data.website.trim())
        ? `https://${data.website.trim()}`
        : data.website.trim();
    const body = frozen || { ...data, website };
    setFrozen(body);
    key.current ??= crypto.randomUUID();
    setBusy(true);
    setError("");
    try {
      await api("/donor/public/partner-signup", {
        method: "POST",
        headers: { "Idempotency-Key": key.current },
        body: JSON.stringify(body),
      });
      setDone(true);
    } catch (cause) {
      if (cause instanceof ApiError && [409, 422].includes(cause.status)) {
        setFrozen(null);
        key.current = null;
        setError(failureMessage(cause, "Check the form fields, then submit again."));
      } else setError(unconfirmedMessage(cause, "Your submission is not confirmed. Retry the same form."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Page eyebrow="Partners" title="Partner with DAD" heading={heading}>
      <p className="lead">
        Help shape initiatives for our April 2027 launch. This is an interest form; partner accounts arrive at R2.
      </p>
      <p className="note">
        Sandbox preview: use test contact details. <Link href="/legal">Legal and privacy status</Link>.
      </p>
      {!done && (
        <form onSubmit={submit}>
          <fieldset disabled={busy || frozen !== null}>
            <legend>Organization and contact</legend>
            <label>
              Organization
              <input
                value={data.organization}
                maxLength={200}
                required
                onChange={(event) => setData({ ...data, organization: event.target.value })}
              />
            </label>
            <label>
              Your name
              <input
                value={data.name}
                maxLength={100}
                required
                onChange={(event) => setData({ ...data, name: event.target.value })}
              />
            </label>
            <label>
              Email
              <input
                type="email"
                value={data.email}
                maxLength={320}
                required
                onChange={(event) => setData({ ...data, email: event.target.value })}
              />
            </label>
            <label>
              Organization website (optional)
              <input
                type="text"
                inputMode="url"
                autoComplete="url"
                placeholder="example.org"
                value={data.website}
                maxLength={500}
                onChange={(event) => setData({ ...data, website: event.target.value })}
              />
            </label>
            <label>
              Tell us about your work (optional)
              <textarea
                value={data.message}
                maxLength={2000}
                onChange={(event) => setData({ ...data, message: event.target.value })}
              />
            </label>
            <label className="choice">
              <input
                type="checkbox"
                checked={data.contact_consent}
                required
                onChange={(event) => setData({ ...data, contact_consent: event.target.checked })}
              />{" "}
              DAD may contact me about this inquiry. This does not subscribe me to marketing.
            </label>
          </fieldset>
          <button className="button" disabled={busy} type="submit">
            {busy ? "Submitting…" : frozen ? "Retry original inquiry" : "Submit partner interest"}
          </button>
        </form>
      )}
      <Announcer
        status={done ? "Your test inquiry was received. This does not confirm a partnership or promise funding." : ""}
        alert={error}
      />
    </Page>
  );
}

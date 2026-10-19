"use client";

import { useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInForm } from "@/components/donor/SignInForm";
import { ApiError, api, type Schema, unconfirmedMessage } from "@/lib/api";
import { formatDateTime } from "@/lib/dates";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

const unconfirmed =
  "Your update is not confirmed yet. You can continue the Stripe flow or refresh after completing it.";

const states: Partial<Record<Schema<"PaymentMethodStatus">["status"], string>> = {
  PENDING: unconfirmed,
  OPEN: unconfirmed,
  EXPIRED: "The Stripe session expired. Start a new update below.",
  CANCELLED: "The monthly gift ended before this update could be applied.",
};

export default function PaymentMethod() {
  const [signedOut, setSignedOut] = useState(false);
  const key = useRef<string | null>(null);
  const { resource, data, failed, reload, setData } = useApiResource<Schema<"PaymentMethodStatus">>(
    signedOut ? null : "/donor/me/payment-method",
    {
      onLoad: () => {
        key.current = null;
        setError("");
      },
    },
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const messages = resourceMessages(resource, {
    loading: "Checking update status…",
    failure: "We could not check your payment-method update right now. Please refresh in a moment.",
  });
  async function begin() {
    if (busy) return;
    setBusy(true);
    setError("");
    key.current ??= crypto.randomUUID();
    try {
      const result = await api<Schema<"PaymentUpdate">>("/donor/me/payment-method/setup", {
        method: "POST",
        headers: { "Idempotency-Key": key.current },
      });
      if (result.url === null) {
        setData((current) => ({ ...current, status: result.status, completed_at: result.completed_at }));
        key.current = null;
        return;
      }
      const url = new URL(result.url);
      if (url.protocol !== "https:" || url.host !== "checkout.stripe.com" || url.username || url.password) {
        setError(
          "We could not open Stripe: it returned an unexpected address. Retry, or refresh to check whether the update has completed.",
        );
        return;
      }
      window.location.assign(url.href);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setSignedOut(true);
      else
        setError(
          unconfirmedMessage(
            cause,
            "We could not open Stripe. Retry, or refresh to check whether the update has completed.",
          ),
        );
    } finally {
      setBusy(false);
    }
  }
  if (signedOut || resource.state === "signedOut")
    return (
      <Page eyebrow="My account" title="Update payment method">
        <p className="lead">
          Keep your monthly gift going. To protect your account, we’ll email you a secure sign-in link first.
        </p>
        <p>Use the email address you gave with your gift. The link brings you straight back here.</p>
        <SignInForm next="/account/payment-method" />
        <p className="note">Sandbox only. Card details are entered on Stripe and never reach DAD.</p>
      </Page>
    );

  const pending = data?.status === "PENDING" || data?.status === "OPEN";
  return (
    <Page eyebrow="My account" title="Update payment method" back={{ href: "/account/giving", label: "My giving" }}>
      <p className="lead">
        Stripe securely collects the card for your future monthly gifts. Card details never reach DAD.
      </p>
      {data?.status === "APPLIED" && (
        <p className="callout">
          Your last payment-method update was confirmed
          {data.completed_at && (
            <>
              {" "}
              on <time dateTime={data.completed_at}>{formatDateTime(data.completed_at)}</time>
            </>
          )}
          .
        </p>
      )}
      {data && states[data.status] && <p className="callout">{states[data.status]}</p>}
      {data && (
        <>
          <button type="button" className="button full" disabled={busy} onClick={begin}>
            {busy ? "Opening Stripe…" : "Continue to Stripe"}
          </button>
          <p className="note">
            Updating a card does not confirm payment of an overdue gift or restore your streak. Those change only after
            a successful charge. Sandbox only.
          </p>
        </>
      )}
      <Announcer status={messages.status} alert={error || messages.alert} />
      {(failed || pending || error) && (
        <button type="button" className="secondary" disabled={busy} onClick={reload}>
          Refresh update status
        </button>
      )}
    </Page>
  );
}

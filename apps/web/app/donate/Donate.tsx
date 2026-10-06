"use client";

import { useEffect, useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, failureMessage } from "@/lib/api";
import { formatDollars, MAX_GIFT_MINOR, MIN_GIFT_MINOR, monthlyFraming, parseDollars } from "@/lib/money";

const presets = [3000, 9000, 15000];
// Remembers this tab's last choice so cancelling Stripe checkout returns to it.
const CHOICE_KEY = "dad.donate.choice";
type Choice = { amount: string; monthly: boolean; email: string };

function remembered(): Choice | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(CHOICE_KEY) ?? "null");
    return typeof value?.amount === "string" && typeof value.monthly === "boolean" && typeof value.email === "string" ? value : null;
  } catch {
    return null;
  }
}

export default function Donate() {
  const [email, setEmail] = useState("");
  const [amount, setAmount] = useState("30");
  const [monthly, setMonthly] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [amountInvalid, setAmountInvalid] = useState(false);
  const amountInput = useRef<HTMLInputElement>(null);
  const attempt = useRef<{ payload: string; key: string } | null>(null);

  useEffect(() => {
    const choice = remembered();
    if (choice) { setAmount(choice.amount); setMonthly(choice.monthly); setEmail(choice.email); }
    // Returning with Back from Stripe can restore this page from the back/forward cache
    // mid-redirect; it must not stay stuck on "Opening secure checkout".
    const reopen = (event: PageTransitionEvent) => { if (event.persisted) setBusy(false); };
    window.addEventListener("pageshow", reopen);
    return () => window.removeEventListener("pageshow", reopen);
  }, []);

  function edit(update: () => void) {
    update();
    setError("");
    setAmountInvalid(false);
  }

  function rejectAmount(message: string) {
    setError(message);
    setAmountInvalid(true);
    amountInput.current?.focus();
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setError("");
    setAmountInvalid(false);
    const minor = parseDollars(amount);
    if (minor === null) return rejectAmount("Enter a dollar amount with no more than two decimal places, like 30 or 30.50.");
    if (minor < MIN_GIFT_MINOR || minor > MAX_GIFT_MINOR) return rejectAmount("Enter an amount between $1 and $999,999.99.");
    const payload = JSON.stringify({ email, amount_minor: minor, frequency: monthly ? "MONTHLY" : "ONE_TIME" });
    if (attempt.current?.payload !== payload) attempt.current = { payload, key: crypto.randomUUID() };
    try { sessionStorage.setItem(CHOICE_KEY, JSON.stringify({ amount, monthly, email } satisfies Choice)); } catch {}
    setBusy(true);
    let destination: URL;
    try {
      const result = await api<{ url: string }>("/donor/checkouts", {
        method: "POST", headers: { "Idempotency-Key": attempt.current.key }, body: payload,
      });
      destination = new URL(result.url);
    } catch (cause) {
      setError(failureMessage(cause, "Checkout could not be opened. Please retry."));
      setBusy(false);
      return;
    }
    if (destination.protocol !== "https:" || destination.host !== "checkout.stripe.com" || destination.username || destination.password) {
      setError("Checkout returned an unexpected destination. No payment was started; please retry.");
      setBusy(false);
      return;
    }
    window.location.assign(destination.href);
  }

  // autoComplete="off" stops the browser restoring stale control values on Back, which
  // would show one frequency while React state holds the other.
  return <Page eyebrow="Give" title="Join the beginning">
    <p className="lead">DAD launches in April 2027. A first gift before launch earns a permanent Founding Donor badge.</p>
    <form onSubmit={submit} autoComplete="off">
      <fieldset disabled={busy}>
        <legend>How often</legend>
        <div className="segmented">
          <label><input type="radio" name="frequency" checked={monthly} onChange={() => edit(() => setMonthly(true))} />Monthly</label>
          <label><input type="radio" name="frequency" checked={!monthly} onChange={() => edit(() => setMonthly(false))} />One time</label>
        </div>
      </fieldset>
      <fieldset disabled={busy}>
        <legend>Choose an amount</legend>
        <div className="presets">
          {presets.map((minor) => {
            const { monthly: perMonth, daily } = monthlyFraming(minor);
            const value = String(minor / 100);
            return <button key={minor} type="button" aria-pressed={parseDollars(amount) === minor}
              onClick={() => edit(() => setAmount(value))}>
              {monthly ? <><span>{perMonth}</span> · <span>{daily}</span></> : `${formatDollars(minor)} once`}
            </button>;
          })}
        </div>
        <label>{monthly ? "Monthly amount (USD)" : "One-time amount (USD)"}
          <input ref={amountInput} inputMode="decimal" required value={amount}
            aria-invalid={amountInvalid || undefined} aria-describedby={amountInvalid ? "donate-error" : undefined}
            onChange={(event) => edit(() => setAmount(event.target.value))} />
        </label>
      </fieldset>
      <label>Email
        <input type="email" autoComplete="email" maxLength={320} required value={email} disabled={busy}
          aria-describedby="donate-email-hint" onChange={(event) => edit(() => setEmail(event.target.value))} />
      </label>
      <p id="donate-email-hint" className="note">For your receipt and the link to claim your account. No account or password needed.</p>
      <Announcer id="donate-error" status={busy ? "Opening secure checkout…" : ""} alert={error} />
      <button className="button full" disabled={busy} type="submit">
        {busy ? "Opening secure checkout…" : "Continue to test checkout"}
      </button>
      <p className="note">You’ll enter card details on Stripe’s secure checkout, never on DAD. This sandbox preview collects no real donation.</p>
    </form>
  </Page>;
}

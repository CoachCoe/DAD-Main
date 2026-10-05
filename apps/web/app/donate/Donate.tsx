"use client";

import { useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";
import { formatDollars, MAX_GIFT_MINOR, MIN_GIFT_MINOR, monthlyFraming, parseDollars } from "@/lib/money";

const presets = [3000, 9000, 15000];

export default function Donate() {
  const [email, setEmail] = useState("");
  const [amount, setAmount] = useState("30");
  const [monthly, setMonthly] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [amountInvalid, setAmountInvalid] = useState(false);
  const amountInput = useRef<HTMLInputElement>(null);
  const attempt = useRef<{ payload: string; key: string } | null>(null);

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
    if (minor === null) return rejectAmount("Enter a dollar amount with no more than two decimal places.");
    if (minor < MIN_GIFT_MINOR || minor > MAX_GIFT_MINOR) return rejectAmount("Enter an amount between $1 and $999,999.99.");
    const payload = JSON.stringify({ email, amount_minor: minor, frequency: monthly ? "MONTHLY" : "ONE_TIME" });
    if (attempt.current?.payload !== payload) {
      attempt.current = { payload, key: crypto.randomUUID() };
    }
    setBusy(true);
    let destination: URL;
    try {
      const result = await api<{ url: string }>("/donor/checkouts", {
        method: "POST", headers: { "Content-Type": "application/json",
          "Idempotency-Key": attempt.current.key }, body: payload,
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

  return (
    <div className="authLayout">
      <form className="panel authPanel" onSubmit={submit}>
        <span className="eyebrow">DAD · TEST CHECKOUT</span>
        <h1>Join the beginning</h1>
        <p>DAD launches in April 2027. A qualifying gift before launch earns a permanent Founding Donor badge.</p>
        <p className="note">This is a sandbox preview. No real donation is collected or badge awarded.</p>
        <fieldset disabled={busy}>
          <legend>Gift frequency</legend>
          <label className="choice"><input type="radio" name="frequency" checked={monthly}
            onChange={() => setMonthly(true)} /> Monthly</label>
          <label className="choice"><input type="radio" name="frequency" checked={!monthly}
            onChange={() => setMonthly(false)} /> One time</label>
        </fieldset>
        <fieldset disabled={busy}>
          <legend>Choose an amount</legend>
          <div className="presets">
            {presets.map((minor) => {
              const { monthly: perMonth, daily } = monthlyFraming(minor);
              const value = String(minor / 100);
              return <button key={minor} type="button" aria-pressed={amount === value}
                onClick={() => { setAmount(value); setAmountInvalid(false); }}>
                {monthly ? <><span>{perMonth}</span> · <span>{daily}</span></> : `${formatDollars(minor)} once`}
              </button>;
            })}
          </div>
          <label className="field">{monthly ? "Monthly amount (USD)" : "One-time amount (USD)"}
            <input ref={amountInput} inputMode="decimal" required value={amount}
              aria-invalid={amountInvalid || undefined} aria-describedby={amountInvalid ? "donate-error" : undefined}
              onChange={(event) => { setAmount(event.target.value); setAmountInvalid(false); }} />
          </label>
          <label className="field">Email
            <input type="email" autoComplete="email" maxLength={320} required value={email}
              onChange={(event) => setEmail(event.target.value)} />
          </label>
        </fieldset>
        <p>No account is required. Card details are entered on Stripe’s hosted checkout.</p>
        <Announcer id="donate-error" status={busy ? "Opening secure checkout…" : ""} alert={error} />
        <button className="button full" disabled={busy} type="submit">
          {busy ? "Opening secure checkout…" : "Continue to test checkout"}
        </button>
      </form>
    </div>
  );
}

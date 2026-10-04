"use client";

import { useRef, useState } from "react";
import { api } from "@/lib/api";

export default function DonatePage() {
  const [email, setEmail] = useState("");
  const [amount, setAmount] = useState("30");
  const [monthly, setMonthly] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const attempt = useRef<{ payload: string; key: string } | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setError(null);
    if (!/^\d+(\.\d{1,2})?$/.test(amount)) {
      setError("Enter a dollar amount with no more than two decimal places.");
      return;
    }
    const [dollars, cents = ""] = amount.split(".");
    const minor = Number(dollars) * 100 + Number(cents.padEnd(2, "0"));
    if (!Number.isSafeInteger(minor) || minor < 100 || minor > 99_999_999) {
      setError("Enter an amount between $1 and $999,999.99.");
      return;
    }
    const payload = JSON.stringify({ email, amount_minor: minor,
      frequency: monthly ? "MONTHLY" : "ONE_TIME" });
    if (attempt.current?.payload !== payload) {
      attempt.current = { payload, key: crypto.randomUUID() };
    }
    setBusy(true);
    try {
      const result = await api<{ url: string }>("/donor/checkouts", {
        method: "POST", headers: { "Content-Type": "application/json",
          "Idempotency-Key": attempt.current.key }, body: payload,
      });
      const destination = new URL(result.url);
      if (destination.protocol !== "https:" || destination.host !== "checkout.stripe.com" || destination.username || destination.password) {
        throw new Error("Checkout returned an unexpected destination.");
      }
      window.location.assign(destination.href);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Checkout could not be opened. Please retry.");
      setBusy(false);
    }
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
          <div className="uploadActions">
            {[30, 90, 150].map((value) => (
              <button key={value} type="button" className="secondary"
                aria-pressed={amount === String(value)} onClick={() => setAmount(String(value))}>
                {monthly ? `$${value}/month · about $${value / 30} a day` : `$${value} once`}
              </button>
            ))}
          </div>
          <label className="field">{monthly ? "Monthly amount (USD)" : "One-time amount (USD)"}
            <input inputMode="decimal" required value={amount}
              onChange={(event) => setAmount(event.target.value)} />
          </label>
          <label className="field">Email
            <input type="email" autoComplete="email" maxLength={320} required value={email}
              onChange={(event) => setEmail(event.target.value)} />
          </label>
        </fieldset>
        <p>No account is required. Card details are entered on Stripe’s hosted checkout.</p>
        {error && <p className="errorMessage" role="alert">{error}</p>}
        <button className="button full" disabled={busy} type="submit">
          {busy ? "Opening secure checkout…" : "Continue to test checkout"}
        </button>
      </form>
    </div>
  );
}

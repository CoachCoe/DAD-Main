"use client";

import { useEffect, useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { GiftFraming } from "@/components/donor/GiftFraming";
import { Page } from "@/components/donor/Page";
import { api, failureMessage, type RequestBody, type Schema } from "@/lib/api";
import { dailyGiftMinor, MAX_DAILY_DOLLARS, monthlyFraming } from "@/lib/money";

const presets = [1, 3, 5];
// Remembers this tab's last choice so cancelling Stripe checkout returns to it.
const CHOICE_KEY = "dad.donate.choice";
type Choice = { amount: string; email: string; name?: string; zip?: string; city?: string };

function remembered(): Choice | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(CHOICE_KEY) ?? "null");
    return typeof value?.amount === "string" && typeof value.email === "string" ? value : null;
  } catch {
    return null;
  }
}

export default function Donate() {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [zip, setZip] = useState("");
  const [city, setCity] = useState("");
  const [amount, setAmount] = useState("1");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [amountInvalid, setAmountInvalid] = useState(false);
  const amountInput = useRef<HTMLInputElement>(null);
  const attempt = useRef<{ payload: string; key: string } | null>(null);
  const chosen = dailyGiftMinor(amount);

  useEffect(() => {
    const choice = remembered();
    if (choice) {
      setAmount(choice.amount);
      setEmail(choice.email);
      setName(choice.name ?? "");
      setZip(choice.zip ?? "");
      setCity(choice.city ?? "");
    }
    // Returning with Back from Stripe can restore this page from the back/forward cache
    // mid-redirect; it must not stay stuck on "Opening secure checkout".
    const reopen = (event: PageTransitionEvent) => {
      if (event.persisted) setBusy(false);
    };
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
    const minor = dailyGiftMinor(amount);
    if (minor === null)
      return rejectAmount(
        `Enter a whole number of dollars a day from $1 to $${MAX_DAILY_DOLLARS}. Larger gifts are arranged with DAD directly.`,
      );
    const payload = JSON.stringify({
      email,
      name,
      zip,
      city,
      amount_minor: minor,
      frequency: "MONTHLY",
    } satisfies RequestBody<"/donor/checkouts", "post">);
    if (attempt.current?.payload !== payload) attempt.current = { payload, key: crypto.randomUUID() };
    try {
      sessionStorage.setItem(CHOICE_KEY, JSON.stringify({ amount, email, name, zip, city } satisfies Choice));
    } catch {}
    setBusy(true);
    let destination: URL;
    try {
      const result = await api<Schema<"CheckoutResult">>("/donor/checkouts", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.current.key },
        body: payload,
      });
      destination = new URL(result.url);
    } catch (cause) {
      setError(failureMessage(cause, "Checkout could not be opened. Please retry."));
      setBusy(false);
      return;
    }
    if (
      destination.protocol !== "https:" ||
      destination.host !== "checkout.stripe.com" ||
      destination.username ||
      destination.password
    ) {
      setError("Checkout returned an unexpected destination. No payment was started; please retry.");
      setBusy(false);
      return;
    }
    window.location.assign(destination.href);
  }

  // autoComplete="off" stops the browser restoring stale control values on Back, which
  // would show one frequency while React state holds the other.
  return (
    <Page eyebrow="Give" title="Join the beginning">
      <p className="lead">
        Give a dollar a day, billed monthly. A first gift before January 2027 earns a permanent Founding Donor badge.
      </p>
      <form onSubmit={submit} autoComplete="off">
        <fieldset disabled={busy}>
          <legend>Choose your daily gift</legend>
          <div className="presets">
            {presets.map((dollars) => {
              const { monthly: perMonth, daily } = monthlyFraming(dailyGiftMinor(String(dollars)) ?? 0);
              return (
                <button
                  key={dollars}
                  type="button"
                  aria-pressed={amount.trim() === String(dollars)}
                  onClick={() => edit(() => setAmount(String(dollars)))}
                >
                  <span>{daily}</span> · <span>{perMonth}</span>
                </button>
              );
            })}
          </div>
          <label>
            Dollars a day
            <input
              ref={amountInput}
              inputMode="numeric"
              required
              value={amount}
              aria-invalid={amountInvalid || undefined}
              aria-describedby={amountInvalid ? "donate-error" : chosen ? "donate-amount" : undefined}
              onChange={(event) => edit(() => setAmount(event.target.value))}
            />
          </label>
          {chosen && (
            <p id="donate-amount">
              You’ll give <GiftFraming minor={chosen} />
            </p>
          )}
        </fieldset>
        <label>
          Name
          <input
            autoComplete="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={150}
            disabled={busy}
            required
          />
        </label>
        <label>
          ZIP code
          <input
            autoComplete="postal-code"
            value={zip}
            onChange={(e) => setZip(e.target.value)}
            pattern="[0-9]{5}(-[0-9]{4})?"
            disabled={busy}
            required
          />
        </label>
        <label>
          City
          <input
            autoComplete="address-level2"
            value={city}
            onChange={(e) => setCity(e.target.value)}
            maxLength={80}
            disabled={busy}
            required
          />
        </label>
        <label>
          Email
          <input
            type="email"
            autoComplete="email"
            maxLength={320}
            required
            value={email}
            disabled={busy}
            aria-describedby="donate-email-hint"
            onChange={(event) => edit(() => setEmail(event.target.value))}
          />
        </label>
        <p id="donate-email-hint" className="note">
          For your receipt and the link to claim your account. No account or password needed.
        </p>
        <Announcer id="donate-error" status={busy ? "Opening secure checkout…" : ""} alert={error} />
        <button className="button full" disabled={busy} type="submit">
          {busy ? "Opening secure checkout…" : "Continue to test checkout"}
        </button>
        <p className="note">
          You’ll enter card details on Stripe’s secure checkout, never on DAD. This sandbox preview collects no real
          donation.
        </p>
      </form>
    </Page>
  );
}

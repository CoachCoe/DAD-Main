const formatters = new Map<string, Intl.NumberFormat>();

function formatter(currency: string, fractionDigits: number) {
  const key = `${currency}:${fractionDigits}`;
  let value = formatters.get(key);
  if (!value) {
    value = new Intl.NumberFormat("en-US", { style: "currency", currency,
      minimumFractionDigits: fractionDigits, maximumFractionDigits: 2 });
    formatters.set(key, value);
  }
  return value;
}

/** Integer minor units to a full currency string, e.g. 3000 → "$30.00". */
export function formatMoney(minor: number, currency = "USD"): string {
  return formatter(currency, 2).format(minor / 100);
}

/** Like formatMoney but drops ".00" from whole amounts, e.g. 3000 → "$30", 4550 → "$45.50". */
export function formatDollars(minor: number, currency = "USD"): string {
  return formatter(currency, minor % 100 === 0 ? 0 : 2).format(minor / 100);
}

/** The monthly charge and its daily framing, which must always be shown at equal weight. */
export function monthlyFraming(minor: number): { monthly: string; daily: string } {
  return { monthly: `${formatDollars(minor)}/month`, daily: `about ${formatDollars(Math.round(minor / 30))} a day` };
}

export const MIN_GIFT_MINOR = 100;
export const MAX_GIFT_MINOR = 99_999_999;

/**
 * Parses a typed dollar amount into exact integer cents. Accepts what people naturally type
 * ("$30", "1,000", "30.", "30.5") but never more than two decimal places.
 */
export function parseDollars(input: string): number | null {
  const match = /^\$?(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d{0,2}))?$/.exec(input.trim());
  if (!match) return null;
  const minor = Number(match[1].replaceAll(",", "")) * 100 + Number((match[2] ?? "").padEnd(2, "0"));
  return Number.isSafeInteger(minor) ? minor : null;
}

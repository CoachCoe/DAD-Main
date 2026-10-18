import { formatDollars, monthlyFraming } from "@/lib/money";

/** A gift as it must be stated: a monthly charge always beside its daily framing, at equal weight. */
export function GiftFraming({ minor, monthly = true }: { minor: number; monthly?: boolean }) {
  if (!monthly)
    return (
      <span className="framing">
        <strong>{formatDollars(minor)} once</strong>
      </span>
    );
  const framing = monthlyFraming(minor);
  return (
    <span className="framing">
      <strong>{framing.monthly}</strong> <span>·</span> <strong>{framing.daily}</strong>
    </span>
  );
}

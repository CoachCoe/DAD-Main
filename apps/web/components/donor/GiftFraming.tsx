import { monthlyFraming } from "@/lib/money";

/** A gift as it must be stated: a monthly charge always beside its daily framing, at equal weight. */
export function GiftFraming({ minor }: { minor: number }) {
  const framing = monthlyFraming(minor);
  return (
    <span className="framing">
      <strong>{framing.monthly}</strong> <span>·</span> <strong>{framing.daily}</strong>
    </span>
  );
}

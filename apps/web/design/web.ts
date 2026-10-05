import type { CSSProperties } from "react";
import { tokens } from "./tokens";

export const webTokenVariables = {
  ...Object.fromEntries(Object.entries(tokens.color).map(([name, value]) => [`--${name}`, value])),
  "--radius-card": `${tokens.radius.card}px`,
  "--radius-pill": `${tokens.radius.pill}px`,
} as CSSProperties;

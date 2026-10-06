import type { CSSProperties } from "react";
import { tokens } from "./tokens";

const lengths = (prefix: string, scale: Record<string, number>) =>
  Object.entries(scale).map(([name, value]) => [`--${prefix}-${name}`, `${value}px`]);

export const webTokenVariables = Object.fromEntries([
  ...Object.entries(tokens.color).map(([name, value]) => [`--${name}`, value]),
  ...lengths("radius", tokens.radius),
  ...lengths("space", tokens.space),
  ...lengths("text", tokens.text),
]) as CSSProperties;

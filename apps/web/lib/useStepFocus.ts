"use client";

import { useEffect, useRef } from "react";

/**
 * When a confirmation step replaces the control that opened it, the focused element
 * disappears and keyboard users are dropped at the top of the document. Move focus to
 * the step's heading on entry and back to the opening control on return.
 */
export function useStepFocus<Trigger extends HTMLElement = HTMLButtonElement>(active: boolean) {
  const heading = useRef<HTMLHeadingElement>(null);
  const trigger = useRef<Trigger>(null);
  const previous = useRef(active);
  useEffect(() => {
    if (active === previous.current) return;
    previous.current = active;
    (active ? heading : trigger).current?.focus();
  }, [active]);
  return { heading, trigger };
}

"use client";

import Link from "next/link";
import type { ComponentProps } from "react";
import { observe } from "@/lib/analytics";

/** The web's form of the app's first-launch choice (PRD D-1): donor and partner accounts are
 * separate identities, so the link a visitor takes on Home is the role they chose. */
export function RoleLink(props: ComponentProps<typeof Link>) {
  return <Link {...props} onClick={() => observe("role_selected")} />;
}

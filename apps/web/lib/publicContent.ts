import { connection } from "next/server";
import { donorApiBase } from "./donorApi";

export type Initiative = { slug: string; title: string; summary: string; partner: string; planned_start: string };
export type LedgerFigure = { id: string; metric: string; value: number; unit: string; illustrative: boolean };
export type PublicContent = {
  state: "unpublished" | "published"; initiatives: Initiative[];
  ledger: LedgerFigure[]; ledger_as_of: string | null; cadence: string | null;
};

/**
 * The published snapshot, or null when the API cannot supply one. Rendering waits for a
 * request because the API address is runtime configuration, but the response itself is
 * shared for a minute: staff publication promises public pages refresh within that.
 */
export async function loadPublicContent(): Promise<PublicContent | null> {
  await connection();
  try {
    const response = await fetch(`${donorApiBase()}/donor/public/content`, { next: { revalidate: 60 } });
    return response.ok ? await response.json() as PublicContent : null;
  } catch {
    return null;
  }
}

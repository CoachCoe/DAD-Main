import { connection } from "next/server";
import type { Schema } from "./api";
import { donorApiBase } from "./donorApi";

/**
 * The published snapshot, or null when the API cannot supply one. Rendering waits for a
 * request because the API address is runtime configuration, but the response itself is
 * shared for a minute: staff publication promises public pages refresh within that.
 */
export async function loadPublicContent(): Promise<Schema<"PublicContent"> | null> {
  await connection();
  try {
    const response = await fetch(`${donorApiBase()}/donor/public/content`, {
      next: { revalidate: 60 },
      signal: AbortSignal.timeout(3000),
    });
    return response.ok ? ((await response.json()) as Schema<"PublicContent">) : null;
  } catch {
    return null;
  }
}

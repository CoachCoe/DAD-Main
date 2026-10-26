import { connection } from "next/server";
import type { Schema } from "./api";
import { donorApiBase } from "./donorApi";

/**
 * The published snapshot, or null when the API cannot supply one. Rendering waits for a
 * request because the API address is runtime configuration, but the response itself is
 * shared for a minute: staff publication promises public pages refresh within that.
 */
export async function loadPublicContent(): Promise<Schema<"PublicContent"> | null> {
  const response = await loadPublic("/donor/public/content");
  return response?.ok ? ((await response.json()) as Schema<"PublicContent">) : null;
}

/** Every published initiative, or null when the API cannot supply them. */
export async function loadInitiatives(): Promise<Schema<"PublicInitiative">[] | null> {
  const response = await loadPublic("/initiatives");
  return response?.ok ? ((await response.json()) as Schema<"PublicInitiative">[]) : null;
}

/** One published initiative; undefined when none is published under the slug, null when unavailable. */
export async function loadInitiative(slug: string): Promise<Schema<"PublicInitiative"> | undefined | null> {
  const response = await loadPublic(`/initiatives/${encodeURIComponent(slug)}`);
  if (response?.status === 404) return undefined;
  return response?.ok ? ((await response.json()) as Schema<"PublicInitiative">) : null;
}

async function loadPublic(path: string): Promise<Response | null> {
  await connection();
  try {
    return await fetch(`${donorApiBase()}${path}`, { next: { revalidate: 60 }, signal: AbortSignal.timeout(3000) });
  } catch {
    return null;
  }
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
  ) {
    super(message);
  }
}

async function toError(response: Response): Promise<ApiError> {
  const payload = await response.json().catch(() => null);
  const detail = payload?.detail;
  const message =
    (typeof detail === "string" ? detail : detail?.message) ??
    `Request failed (${response.status})`;
  return new ApiError(message, response.status, detail?.code);
}

/** Browser-side call. Always sends the session cookie. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = init.headers instanceof Headers || Array.isArray(init.headers)
    ? Object.fromEntries(init.headers) : { ...init.headers };
  if (typeof init.body === "string" && !Object.keys(headers).some((name) => name.toLowerCase() === "content-type")) {
    headers["Content-Type"] = "application/json";
  }
  const response = await fetch(`/api${path}`, { ...init, headers, credentials: "include" });
  if (!response.ok) throw await toError(response);
  return response.json() as Promise<T>;
}

export type Failure = "signedOut" | "forbidden" | "notFound" | "rateLimited" | "unavailable" | "rejected" | "offline";

export function classifyFailure(cause: unknown): Failure {
  if (cause instanceof ApiError) {
    if (cause.status === 401) return "signedOut";
    if (cause.status === 403) return "forbidden";
    if (cause.status === 404) return "notFound";
    if (cause.status === 429) return "rateLimited";
    return cause.status >= 500 ? "unavailable" : "rejected";
  }
  // fetch rejects only when no response arrived; an unreadable body means the server answered.
  return cause instanceof SyntaxError ? "unavailable" : "offline";
}

export const OFFLINE_MESSAGE = "You appear to be offline. Check your connection, then retry.";
export const RATE_LIMITED_MESSAGE = "Too many requests from this connection. Wait a minute, then retry.";

/** Copy safe to show a donor: the API's own explanation for a rejected request, never a transport error. */
export function failureMessage(cause: unknown, fallback: string): string {
  const failure = classifyFailure(cause);
  if (failure === "offline") return OFFLINE_MESSAGE;
  if (failure === "rateLimited") return RATE_LIMITED_MESSAGE;
  return cause instanceof ApiError && failure !== "unavailable" ? cause.message : fallback;
}

/** For a command whose outcome is unknown: keeps the "not confirmed" statement and adds why. */
export function unconfirmedMessage(cause: unknown, statement: string): string {
  const failure = classifyFailure(cause);
  if (failure === "offline") return `${statement} ${OFFLINE_MESSAGE}`;
  if (failure === "rateLimited") return `${statement} ${RATE_LIMITED_MESSAGE}`;
  return failureMessage(cause, statement);
}

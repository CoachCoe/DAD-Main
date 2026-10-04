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

"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api, classifyFailure, OFFLINE_MESSAGE, RATE_LIMITED_MESSAGE, UNCONFIGURED_MESSAGE } from "./api";

export type Resource<T> =
  | { state: "loading" }
  | { state: "ready"; data: T }
  | { state: "signedOut" }
  | { state: "forbidden" }
  | { state: "notFound" }
  | { state: "offline" }
  | { state: "unconfigured" }
  | { state: "error"; rateLimited: boolean };

type Options<T> = {
  /** Replaces the default GET of `path`, for pages that compose several requests. */
  load?: (signal: AbortSignal) => Promise<T>;
  /** Runs once per successful load, before the page renders the data. */
  onLoad?: (data: T) => void;
};

/**
 * Loads `path` (or nothing while it is null) and reports every screen state the
 * product requires. Changing `path` or calling `reload` starts a fresh request and
 * abandons any in flight.
 */
export function useApiResource<T>(path: string | null, options: Options<T> = {}) {
  const [resource, setResource] = useState<Resource<T>>({ state: "loading" });
  const [generation, setGeneration] = useState(0);
  const latest = useRef(options);
  useEffect(() => {
    latest.current = options;
  });
  // biome-ignore lint/correctness/useExhaustiveDependencies: generation is the reload trigger, deliberately unread
  useEffect(() => {
    if (path === null) return;
    const controller = new AbortController();
    setResource({ state: "loading" });
    const { load = (signal: AbortSignal) => api<T>(path, { signal }) } = latest.current;
    load(controller.signal)
      .then((data) => {
        if (controller.signal.aborted) return;
        latest.current.onLoad?.(data);
        setResource({ state: "ready", data });
      })
      .catch((cause) => {
        if (controller.signal.aborted) return;
        const failure =
          typeof navigator !== "undefined" && navigator.onLine === false ? "offline" : classifyFailure(cause);
        if (
          failure === "signedOut" ||
          failure === "forbidden" ||
          failure === "notFound" ||
          failure === "offline" ||
          failure === "unconfigured"
        )
          setResource({ state: failure });
        else setResource({ state: "error", rateLimited: failure === "rateLimited" });
      });
    return () => controller.abort();
  }, [path, generation]);
  const reload = useCallback(() => setGeneration((value) => value + 1), []);
  const setData = useCallback(
    (update: T | ((current: T) => T)) =>
      setResource((current) =>
        current.state !== "ready"
          ? current
          : {
              state: "ready",
              data: typeof update === "function" ? (update as (current: T) => T)(current.data) : update,
            },
      ),
    [],
  );
  return {
    resource,
    data: resource.state === "ready" ? resource.data : null,
    failed: resource.state === "offline" || resource.state === "error",
    reload,
    setData,
  };
}

/** The live-region copy for a resource that is not ready. */
export function resourceMessages(
  resource: Resource<unknown>,
  copy: { loading: string; failure: string; forbidden?: string; notFound?: string },
) {
  switch (resource.state) {
    case "loading":
      return { status: copy.loading, alert: "" };
    case "offline":
      return { status: "", alert: OFFLINE_MESSAGE };
    case "unconfigured":
      return { status: "", alert: UNCONFIGURED_MESSAGE };
    case "forbidden":
      return { status: "", alert: copy.forbidden ?? "You don’t have permission to view this page." };
    case "notFound":
      return { status: "", alert: copy.notFound ?? copy.failure };
    case "error":
      return { status: "", alert: resource.rateLimited ? RATE_LIMITED_MESSAGE : copy.failure };
    default:
      return { status: "", alert: "" };
  }
}

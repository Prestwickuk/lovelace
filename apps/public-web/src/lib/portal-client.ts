import type { ApiErrorEnvelope } from "./types";

export class PortalClientError extends Error {
  status: number;
  code: string;
  fields?: Record<string, string>;

  constructor(
    status: number,
    code: string,
    message: string,
    fields?: Record<string, string>,
  ) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

export async function portalFetch<T>(
  path: string,
  options: {
    method?: string;
    body?: unknown;
    csrfToken?: string;
  } = {},
): Promise<T> {
  const headers: Record<string, string> = {};
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (options.csrfToken) {
    headers["X-CSRF-Token"] = options.csrfToken;
  }

  const res = await fetch(path, {
    method: options.method || "GET",
    headers,
    credentials: "same-origin",
    cache: "no-store",
    referrerPolicy: "no-referrer",
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
  });

  const text = await res.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }

  if (!res.ok) {
    const env = (parsed || {}) as ApiErrorEnvelope;
    throw new PortalClientError(
      res.status,
      env.error?.code || "portal_error",
      env.error?.message || `Request failed (${res.status})`,
      env.error?.fields,
    );
  }

  return parsed as T;
}

export function statusBadgeClass(status: string): string {
  const s = (status || "").toUpperCase();
  if (
    s === "CONFIRMED" ||
    s === "CHECKED_IN" ||
    s === "PAID" ||
    s === "SUCCEEDED" ||
    s === "GRANTED" ||
    s === "RESOLVED"
  ) {
    return "portal-badge portal-badge-ok";
  }
  if (
    s === "PENDING" ||
    s === "PARTIALLY_PAID" ||
    s === "WAITING_FOR_GUEST" ||
    s === "WAITING_FOR_PROPERTY" ||
    s === "IN_PROGRESS"
  ) {
    return "portal-badge portal-badge-warn";
  }
  if (s === "CANCELLED" || s === "NO_SHOW" || s === "FAILED") {
    return "portal-badge portal-badge-err";
  }
  return "portal-badge";
}

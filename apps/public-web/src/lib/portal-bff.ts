import "server-only";

import { NextRequest, NextResponse } from "next/server";
import { PRIVATE_NO_STORE_HEADERS } from "./bff-response";
import { callLovelacePublic, getPropertySlug, LovelaceApiError } from "./lovelace-api";
import { checkBffRateLimit, rateLimitResponse, type BffLimitTier } from "./rate-limit";
import type { PortalSessionScope } from "./types";

export const GUEST_SESSION_COOKIE = "lovelace_guest_session";
export const PORTAL_NONCE_COOKIE = "lovelace_portal_nonce";

const SAFE_BOOKING_REF_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{1,47}$/;
const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
const FORBIDDEN_QUERY_PARAMS = [
  "token",
  "session",
  "session_token",
  "otp",
  "code",
  "magic_token",
  "csrf",
  "csrf_token",
];

function isSecureCookieEnv(): boolean {
  return (
    process.env.NODE_ENV === "production" ||
    process.env.LOVELACE_ENV === "production"
  );
}

function isTrustProxyEnabled(): boolean {
  return (process.env.LOVELACE_TRUST_PROXY || "").trim().toLowerCase() === "true";
}

export function assertNoQuerySecrets(req: NextRequest): void {
  const sp = req.nextUrl.searchParams;
  for (const key of FORBIDDEN_QUERY_PARAMS) {
    if (sp.has(key) && (sp.get(key) || "").trim() !== "") {
      throw new LovelaceApiError(
        400,
        "query_token_forbidden",
        "Portal credentials and verification tokens must never be passed in URL query parameters.",
      );
    }
  }
}

export function assertSafeBookingRef(ref: string): string {
  const clean = (ref || "").trim();
  if (!SAFE_BOOKING_REF_RE.test(clean)) {
    throw new LovelaceApiError(400, "invalid_reference", "Invalid booking reference.");
  }
  return clean;
}

export function assertSafeUuid(id: string, label = "identifier"): string {
  const clean = (id || "").trim();
  if (!UUID_RE.test(clean)) {
    throw new LovelaceApiError(400, "invalid_id", `Invalid ${label}.`);
  }
  return clean;
}

function normalizeOrigin(raw: string | null): string {
  if (!raw) return "";
  try {
    const parsed = new URL(raw);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return "";
    }
    return `${parsed.protocol}//${parsed.host}`.toLowerCase();
  } catch {
    return "";
  }
}

export function assertSameOriginMutation(req: NextRequest): void {
  const reqOrigin = normalizeOrigin(req.headers.get("origin"));
  const refererOrigin = normalizeOrigin(req.headers.get("referer"));
  const sourceOrigin = reqOrigin || refererOrigin;
  if (!sourceOrigin) {
    throw new LovelaceApiError(
      403,
      "origin_missing",
      "Cross-site request protection requires an Origin or Referer header.",
    );
  }

  const allowed = new Set<string>();
  const selfOrigin = normalizeOrigin(req.nextUrl.origin);
  if (selfOrigin) allowed.add(selfOrigin);

  const trustProxy = isTrustProxyEnabled();
  const hostHeader = trustProxy
    ? (req.headers.get("x-forwarded-host") || req.headers.get("host") || "").split(",")[0].trim()
    : (req.headers.get("host") || "").trim();
  const protoHeader = trustProxy
    ? (req.headers.get("x-forwarded-proto") || req.nextUrl.protocol.replace(":", "") || "http")
        .split(",")[0]
        .trim()
    : req.nextUrl.protocol.replace(":", "") || "http";
  if (hostHeader && (protoHeader === "http" || protoHeader === "https")) {
    const hostOrigin = normalizeOrigin(`${protoHeader}://${hostHeader}`);
    if (hostOrigin) allowed.add(hostOrigin);
  }

  const configuredSite = normalizeOrigin(process.env.NEXT_PUBLIC_SITE_URL || null);
  if (configuredSite) allowed.add(configuredSite);

  if (!isSecureCookieEnv()) {
    for (const candidate of Array.from(allowed)) {
      if (candidate.includes("localhost")) {
        allowed.add(candidate.replace("localhost", "127.0.0.1"));
      } else if (candidate.includes("127.0.0.1")) {
        allowed.add(candidate.replace("127.0.0.1", "localhost"));
      }
    }
  }

  if (!allowed.has(sourceOrigin)) {
    throw new LovelaceApiError(
      403,
      "origin_not_allowed",
      "Cross-site portal mutation rejected.",
    );
  }
}

export function enforcePortalBffRateLimit(
  req: NextRequest,
  bucket: string,
  limitPerMinute: number,
): NextResponse | null {
  const tier: BffLimitTier =
    bucket.includes("auth") || bucket.includes("privacy") || limitPerMinute <= 15
      ? "lookup"
      : limitPerMinute <= 30
        ? "write"
        : "read";
  const rl = checkBffRateLimit(req, tier);
  if (!rl.allowed) {
    const res = rateLimitResponse(rl.retryAfterSeconds);
    for (const [k, v] of Object.entries(PRIVATE_NO_STORE_HEADERS)) {
      res.headers.set(k, v);
    }
    return res;
  }
  return null;
}

export function setPortalSessionCookie(
  res: NextResponse,
  sessionToken: string,
  scopeType: PortalSessionScope,
): void {
  // Decision D8: Tier A max 12h absolute; Tier B max 7d absolute.
  const maxAgeSeconds =
    scopeType === "BOOKING" ? 12 * 60 * 60 : 7 * 24 * 60 * 60;
  res.cookies.set({
    name: GUEST_SESSION_COOKIE,
    value: sessionToken,
    httpOnly: true,
    secure: isSecureCookieEnv(),
    sameSite: "lax",
    path: "/",
    maxAge: maxAgeSeconds,
  });
}

export function clearPortalSessionCookie(res: NextResponse): void {
  res.cookies.set({
    name: GUEST_SESSION_COOKIE,
    value: "",
    httpOnly: true,
    secure: isSecureCookieEnv(),
    sameSite: "lax",
    path: "/",
    maxAge: 0,
  });
}

export function setPortalNonceCookie(res: NextResponse, nonce: string): void {
  res.cookies.set({
    name: PORTAL_NONCE_COOKIE,
    value: nonce,
    httpOnly: true,
    secure: isSecureCookieEnv(),
    sameSite: "lax",
    path: "/api/public/portal/auth",
    maxAge: 15 * 60,
  });
}

export function clearPortalNonceCookie(res: NextResponse): void {
  res.cookies.set({
    name: PORTAL_NONCE_COOKIE,
    value: "",
    httpOnly: true,
    secure: isSecureCookieEnv(),
    sameSite: "lax",
    path: "/api/public/portal/auth",
    maxAge: 0,
  });
}

export function buildPropertyPortalPath(subpath: string): string {
  const slug = encodeURIComponent(getPropertySlug());
  const cleanSubpath = subpath.startsWith("/") ? subpath : `/${subpath}`;
  return `/api/public/v1/properties/${slug}/portal${cleanSubpath}`;
}

export async function callLovelacePortal<T>(
  req: NextRequest,
  portalSubpath: string,
  init: RequestInit = {},
  options: { requireSession?: boolean; requireCsrf?: boolean } = {},
): Promise<T> {
  assertNoQuerySecrets(req);

  const requireSession = options.requireSession ?? true;
  const requireCsrf = options.requireCsrf ?? false;

  // Strictly read ONLY lovelace_guest_session; never read admin tirbach_session.
  const sessionToken = (req.cookies.get(GUEST_SESSION_COOKIE)?.value || "").trim();
  if (requireSession && !sessionToken) {
    throw new LovelaceApiError(
      401,
      "unauthorized",
      "Please sign in to access your guest portal.",
    );
  }

  const csrfToken = (
    req.headers.get("x-csrf-token") ||
    req.headers.get("x-guest-csrf") ||
    ""
  ).trim();
  if (requireCsrf && !csrfToken) {
    throw new LovelaceApiError(
      403,
      "csrf_invalid",
      "Missing portal security token. Please refresh the page and try again.",
    );
  }

  const headers = new Headers(init.headers);
  headers.set("X-Lovelace-Property", getPropertySlug());
  if (sessionToken) {
    headers.set("X-Guest-Session", sessionToken);
  }
  if (csrfToken) {
    headers.set("X-Guest-CSRF", csrfToken);
  }

  const rl = checkBffRateLimit(req, "read");
  const upstreamPath = portalSubpath.startsWith("/api/public/v1/")
    ? portalSubpath
    : buildPropertyPortalPath(portalSubpath);
  return callLovelacePublic<T>(upstreamPath, { ...init, headers }, rl.clientIp);
}

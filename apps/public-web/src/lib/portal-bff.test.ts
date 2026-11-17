import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("server-only", () => ({}));

import { NextRequest, NextResponse } from "next/server";
import {
  assertNoQuerySecrets,
  assertSafeBookingRef,
  assertSafeUuid,
  assertSameOriginMutation,
  buildPropertyPortalPath,
  callLovelacePortal,
  enforcePortalBffRateLimit,
  GUEST_SESSION_COOKIE,
  PORTAL_NONCE_COOKIE,
  setPortalNonceCookie,
  setPortalSessionCookie,
} from "./portal-bff";
import { parseJsonObject } from "./bff-response";
import { portalFetch, statusBadgeClass } from "./portal-client";

describe("Guest Portal BFF security & boundary helpers", () => {
  const originalTrustProxy = process.env.LOVELACE_TRUST_PROXY;
  const originalTrustedProxies = process.env.LOVELACE_TRUSTED_PROXIES;
  const originalSiteUrl = process.env.NEXT_PUBLIC_SITE_URL;

  afterEach(() => {
    if (originalTrustProxy === undefined) {
      delete process.env.LOVELACE_TRUST_PROXY;
    } else {
      process.env.LOVELACE_TRUST_PROXY = originalTrustProxy;
    }
    if (originalTrustedProxies === undefined) {
      delete process.env.LOVELACE_TRUSTED_PROXIES;
    } else {
      process.env.LOVELACE_TRUSTED_PROXIES = originalTrustedProxies;
    }
    if (originalSiteUrl === undefined) {
      delete process.env.NEXT_PUBLIC_SITE_URL;
    } else {
      process.env.NEXT_PUBLIC_SITE_URL = originalSiteUrl;
    }
  });

  it("validates booking references and UUIDs strictly against path traversal", () => {
    expect(assertSafeBookingRef("TB-2026-AB12CD")).toBe("TB-2026-AB12CD");
    expect(() => assertSafeBookingRef("../admin")).toThrow();
    expect(() => assertSafeBookingRef("TB 2026")).toThrow();

    const validId = "550e8400-e29b-41d4-a716-446655440000";
    expect(assertSafeUuid(validId)).toBe(validId);
    expect(() => assertSafeUuid("not-a-uuid")).toThrow();
  });

  it("rejects portal secrets passed in URL query strings", () => {
    const cleanReq = new NextRequest(
      "http://localhost:3000/api/public/portal/bookings?page=1",
    );
    expect(() => assertNoQuerySecrets(cleanReq)).not.toThrow();

    for (const param of [
      "token",
      "session",
      "session_token",
      "otp",
      "code",
      "magic_token",
      "csrf",
      "csrf_token",
    ]) {
      const badReq = new NextRequest(
        `http://localhost:3000/api/public/portal/auth/verify?${param}=secret123`,
        { method: "POST" },
      );
      expect(() => assertNoQuerySecrets(badReq)).toThrow(/query/);
    }
  });

  it("enforces same-origin mutations and blocks spoofed X-Forwarded-Host unless LOVELACE_TRUST_PROXY=true", () => {
    delete process.env.LOVELACE_TRUST_PROXY;

    const okReq = new NextRequest("http://localhost:3000/api/public/portal/consent", {
      method: "POST",
      headers: {
        origin: "http://localhost:3000",
      },
    });
    expect(() => assertSameOriginMutation(okReq)).not.toThrow();

    const forgedReq = new NextRequest("http://localhost:3000/api/public/portal/consent", {
      method: "POST",
      headers: {
        origin: "https://evil.example.org",
      },
    });
    expect(() => assertSameOriginMutation(forgedReq)).toThrow();

    const spoofedForwardedHostReq = new NextRequest(
      "http://localhost:3000/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          origin: "https://evil.example.org",
          "x-forwarded-host": "evil.example.org",
          "x-forwarded-proto": "https",
        },
      },
    );
    expect(() => assertSameOriginMutation(spoofedForwardedHostReq)).toThrow();

    process.env.LOVELACE_TRUST_PROXY = "true";
    process.env.NEXT_PUBLIC_SITE_URL = "https://bookings.tirbach.wales";
    const trustedProxyReq = new NextRequest(
      "http://127.0.0.1:3000/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          host: "127.0.0.1:3000",
          origin: "https://bookings.tirbach.wales",
          "x-forwarded-host": "bookings.tirbach.wales",
          "x-forwarded-proto": "https",
        },
      },
    );
    expect(() => assertSameOriginMutation(trustedProxyReq)).not.toThrow();

    // Even when LOVELACE_TRUST_PROXY=true, spoofed X-Forwarded-Host outside NEXT_PUBLIC_SITE_URL fails closed.
    const spoofedWithTrustProxyReq = new NextRequest(
      "http://127.0.0.1:3000/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          host: "127.0.0.1:3000",
          origin: "https://evil.example.org",
          "x-forwarded-host": "evil.example.org",
          "x-forwarded-proto": "https",
        },
      },
    );
    expect(() => assertSameOriginMutation(spoofedWithTrustProxyReq)).toThrow();

    // Direct spoofed Host header fails closed even when Origin matches spoofed Host.
    process.env.LOVELACE_TRUST_PROXY = "false";
    process.env.NEXT_PUBLIC_SITE_URL = "http://127.0.0.1:3000";
    const spoofedHostHeaderReq = new NextRequest(
      "http://evil.example.org/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          host: "evil.example.org",
          origin: "http://evil.example.org",
        },
      },
    );
    expect(() => assertSameOriginMutation(spoofedHostHeaderReq)).toThrow();

    delete process.env.NEXT_PUBLIC_SITE_URL;
    const missingOriginReq = new NextRequest(
      "http://localhost:3000/api/public/portal/consent",
      { method: "POST" },
    );
    expect(() => assertSameOriginMutation(missingOriginReq)).toThrow();

    // Origin: null must fail closed even if a valid same-origin Referer header is present.
    const nullOriginWithRefererReq = new NextRequest(
      "http://localhost:3000/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          origin: "null",
          referer: "http://localhost:3000/portal/profile",
        },
      },
    );
    expect(() => assertSameOriginMutation(nullOriginWithRefererReq)).toThrow();
  });

  it("buckets IPv6 /64 prefixes in portal BFF rate limiting and rejects non-object JSON bodies", async () => {
    process.env.LOVELACE_TRUST_PROXY = "true";
    process.env.LOVELACE_TRUSTED_PROXIES = "127.0.0.1/32";

    const req1 = new NextRequest("http://localhost:3000/api/public/portal/privacy/sar", {
      method: "POST",
      headers: { "x-forwarded-for": "2001:db8:abcd:1234::1" },
    });
    const req2 = new NextRequest("http://localhost:3000/api/public/portal/privacy/sar", {
      method: "POST",
      headers: { "x-forwarded-for": "2001:db8:abcd:1234::ffff" },
    });
    expect(enforcePortalBffRateLimit(req1, "test-ipv6-bucket-cap", 1)).toBeNull();
    const blocked = enforcePortalBffRateLimit(req2, "test-ipv6-bucket-cap", 1);
    expect(blocked?.status).toBe(429);

    const nullJsonReq = new Request("http://localhost:3000/api/public/portal/consent", {
      method: "POST",
      body: "null",
      headers: { "Content-Type": "application/json" },
    });
    await expect(parseJsonObject(nullJsonReq)).rejects.toMatchObject({
      status: 400,
      code: "invalid_json",
    });
  });

  it("never accepts admin tirbach_session cookie and requires CSRF token on mutations", async () => {
    const adminCookieReq = new NextRequest(
      "http://localhost:3000/api/public/portal/bookings",
      {
        method: "GET",
        headers: {
          cookie: "tirbach_session=admin-secret-token-value",
        },
      },
    );
    await expect(
      callLovelacePortal(adminCookieReq, "/bookings"),
    ).rejects.toMatchObject({ status: 401, code: "unauthorized" });

    const missingCsrfReq = new NextRequest(
      "http://localhost:3000/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          cookie: `${GUEST_SESSION_COOKIE}=guest-session-secret`,
        },
      },
    );
    await expect(
      callLovelacePortal(missingCsrfReq, "/consent", { method: "POST" }, {
        requireSession: true,
        requireCsrf: true,
      }),
    ).rejects.toMatchObject({ status: 403, code: "csrf_invalid" });

    expect(buildPropertyPortalPath("/bookings")).toBe(
      "/api/public/v1/properties/tir-bach/portal/bookings",
    );
  });

  it("sets isolated HttpOnly SameSite=Lax cookies with D8 max-age bounds", () => {
    const resAccount = NextResponse.json({ ok: true });
    setPortalSessionCookie(resAccount, "opaque-token-123", "ACCOUNT");
    setPortalNonceCookie(resAccount, "nonce-456");

    const accountCookie = resAccount.cookies.get(GUEST_SESSION_COOKIE);
    expect(accountCookie?.value).toBe("opaque-token-123");
    expect(accountCookie?.httpOnly).toBe(true);
    expect(accountCookie?.sameSite).toBe("lax");
    expect(accountCookie?.path).toBe("/");
    expect(accountCookie?.maxAge).toBe(7 * 24 * 60 * 60);

    const nonceCookie = resAccount.cookies.get(PORTAL_NONCE_COOKIE);
    expect(nonceCookie?.value).toBe("nonce-456");
    expect(nonceCookie?.httpOnly).toBe(true);
    expect(nonceCookie?.path).toBe("/api/public/portal/auth");
    expect(nonceCookie?.maxAge).toBe(15 * 60);

    const resBooking = NextResponse.json({ ok: true });
    setPortalSessionCookie(resBooking, "booking-token-789", "BOOKING");
    expect(resBooking.cookies.get(GUEST_SESSION_COOKIE)?.maxAge).toBe(12 * 60 * 60);
  });

  it("maps status badge classes cleanly", () => {
    expect(statusBadgeClass("CONFIRMED")).toContain("portal-badge-ok");
    expect(statusBadgeClass("PENDING")).toContain("portal-badge-warn");
    expect(statusBadgeClass("CANCELLED")).toContain("portal-badge-err");
    expect(typeof portalFetch).toBe("function");
  });
});

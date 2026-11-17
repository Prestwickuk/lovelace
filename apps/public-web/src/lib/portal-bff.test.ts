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
  GUEST_SESSION_COOKIE,
  PORTAL_NONCE_COOKIE,
  setPortalNonceCookie,
  setPortalSessionCookie,
} from "./portal-bff";
import { portalFetch, statusBadgeClass } from "./portal-client";

describe("Guest Portal BFF security & boundary helpers", () => {
  const originalTrustProxy = process.env.LOVELACE_TRUST_PROXY;

  afterEach(() => {
    if (originalTrustProxy === undefined) {
      delete process.env.LOVELACE_TRUST_PROXY;
    } else {
      process.env.LOVELACE_TRUST_PROXY = originalTrustProxy;
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
    const trustedProxyReq = new NextRequest(
      "http://127.0.0.1:3000/api/public/portal/consent",
      {
        method: "POST",
        headers: {
          origin: "https://bookings.tirbach.wales",
          "x-forwarded-host": "bookings.tirbach.wales",
          "x-forwarded-proto": "https",
        },
      },
    );
    expect(() => assertSameOriginMutation(trustedProxyReq)).not.toThrow();

    const missingOriginReq = new NextRequest(
      "http://localhost:3000/api/public/portal/consent",
      { method: "POST" },
    );
    expect(() => assertSameOriginMutation(missingOriginReq)).toThrow();
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

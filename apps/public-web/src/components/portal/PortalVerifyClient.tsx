"use client";

import { useEffect, useState, useSyncExternalStore, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { portalFetch, PortalClientError } from "@/lib/portal-client";
import type { PortalSessionView } from "@/lib/types";

let cachedFragmentToken: string | null = null;

function readClientFragmentToken(): string {
  if (typeof window === "undefined") return "";
  if (cachedFragmentToken !== null) return cachedFragmentToken;
  const rawHash = window.location.hash.replace(/^#/, "");
  const params = new URLSearchParams(rawHash);
  cachedFragmentToken = (params.get("t") || "").trim();
  return cachedFragmentToken;
}

function subscribeNoop(): () => void {
  return () => {};
}

export function PortalVerifyClient({
  portalEnabled = false,
}: {
  portalEnabled?: boolean;
}) {
  const router = useRouter();
  const magicToken = useSyncExternalStore(
    subscribeNoop,
    readClientFragmentToken,
    () => "",
  );
  const [preferCodeEntry, setPreferCodeEntry] = useState(false);
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [bookingRef, setBookingRef] = useState("");
  const [needsDisambiguationRef, setNeedsDisambiguationRef] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const fallbackCodeMode = preferCodeEntry || !magicToken;

  useEffect(() => {
    if (typeof window !== "undefined" && window.location.hash) {
      // Scrub fragment from browser URL bar immediately so it cannot be copied or leaked
      window.history.replaceState(null, "", window.location.pathname);
    }
  }, [magicToken]);

  if (!portalEnabled) {
    return (
      <div className="card-panel">
        <h1 className="section-title">Guest Portal</h1>
        <p style={{ color: "var(--text-muted)" }}>
          Online Guest Portal sign-in is not currently enabled for this property.
        </p>
        <Link href="/lookup" className="btn btn-primary">
          Find My Booking
        </Link>
      </div>
    );
  }

  async function onRedeemMagicLink(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await portalFetch<{ session: PortalSessionView }>(
        "/api/public/portal/auth/verify",
        {
          method: "POST",
          body: {
            magic_token: magicToken,
            booking_reference: bookingRef.trim(),
          },
        },
      );
      cachedFragmentToken = "";
      if (
        res.session.scope_type === "BOOKING" &&
        res.session.scoped_booking_reference
      ) {
        router.push(
          `/portal/bookings/${encodeURIComponent(res.session.scoped_booking_reference)}`,
        );
      } else {
        router.push("/portal/bookings");
      }
    } catch (err) {
      if (
        err instanceof PortalClientError &&
        err.code === "duplicate_guest_verification_required"
      ) {
        setNeedsDisambiguationRef(true);
        setError(
          "Multiple guest profiles share this email address. Please enter a booking reference below to confirm your profile.",
        );
      } else {
        setPreferCodeEntry(true);
        setError(
          "This link could not be verified automatically (for example if opened in a different browser or after expiry). Please enter your email address and the 6-digit code from the same email below.",
        );
      }
    } finally {
      setBusy(false);
    }
  }

  async function onRedeemCode(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await portalFetch<{ session: PortalSessionView }>(
        "/api/public/portal/auth/verify",
        {
          method: "POST",
          body: {
            email: email.trim(),
            code: code.trim(),
            booking_reference: bookingRef.trim(),
          },
        },
      );
      cachedFragmentToken = "";
      if (
        res.session.scope_type === "BOOKING" &&
        res.session.scoped_booking_reference
      ) {
        router.push(
          `/portal/bookings/${encodeURIComponent(res.session.scoped_booking_reference)}`,
        );
      } else {
        router.push("/portal/bookings");
      }
    } catch (err) {
      if (
        err instanceof PortalClientError &&
        err.code === "duplicate_guest_verification_required"
      ) {
        setNeedsDisambiguationRef(true);
        setError(
          "Multiple guest profiles share this email address. Please enter a booking reference below to confirm your profile.",
        );
      } else {
        setError(
          err instanceof PortalClientError
            ? err.message
            : "Invalid or expired verification code.",
        );
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card-panel">
      <h1 className="section-title">Complete Guest Portal Sign-In</h1>
      <p style={{ color: "var(--text-muted)" }}>
        To protect your booking privacy against automated email link scanners, sign-in is only
        completed when you confirm below.
      </p>

      {error ? (
        <div
          id="portal-verify-err"
          className="alert alert-err"
          role="alert"
          style={{ marginBottom: "1rem" }}
        >
          {error}
        </div>
      ) : null}

      {!fallbackCodeMode ? (
        <form onSubmit={onRedeemMagicLink} style={{ display: "grid", gap: "1rem" }}>
          {needsDisambiguationRef ? (
            <div className="field">
              <label htmlFor="verify-magic-ref">Booking Reference</label>
              <input
                id="verify-magic-ref"
                type="text"
                required
                value={bookingRef}
                onChange={(e) => setBookingRef(e.target.value)}
                placeholder="e.g. LV-2026-AB12CD"
              />
            </div>
          ) : null}
          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem" }}>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? "Completing Sign-In…" : "Complete Sign-In"}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => setPreferCodeEntry(true)}
            >
              Use 6-Digit Code Instead
            </button>
          </div>
        </form>
      ) : (
        <form onSubmit={onRedeemCode} style={{ display: "grid", gap: "1rem" }}>
          <div className="field">
            <label htmlFor="verify-fallback-email">Email Address</label>
            <input
              id="verify-fallback-email"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
              aria-describedby={error ? "portal-verify-err" : undefined}
            />
          </div>
          {needsDisambiguationRef ? (
            <div className="field">
              <label htmlFor="verify-fallback-ref">Booking Reference</label>
              <input
                id="verify-fallback-ref"
                type="text"
                required
                value={bookingRef}
                onChange={(e) => setBookingRef(e.target.value)}
                placeholder="e.g. LV-2026-AB12CD"
              />
            </div>
          ) : null}
          <div className="field">
            <label htmlFor="verify-fallback-code">6-Digit Verification Code</label>
            <input
              id="verify-fallback-code"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              required
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="123456"
            />
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem" }}>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? "Verifying…" : "Verify & Sign In"}
            </button>
            <Link href="/portal" className="btn btn-secondary">
              Request a New Code
            </Link>
          </div>
        </form>
      )}
    </div>
  );
}

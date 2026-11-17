"use client";

import { useEffect, useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { portalFetch, PortalClientError } from "@/lib/portal-client";
import type { PortalSessionView } from "@/lib/types";

export function PortalLoginClient({
  portalEnabled,
  hasSessionCookie = false,
}: {
  portalEnabled: boolean;
  hasSessionCookie?: boolean;
}) {
  const router = useRouter();
  const [checkingSession, setCheckingSession] = useState(
    portalEnabled && hasSessionCookie,
  );
  const [mode, setMode] = useState<"account" | "booking_token">("account");

  const [email, setEmail] = useState("");
  const [bookingRef, setBookingRef] = useState("");
  const [confirmationToken, setConfirmationToken] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [code, setCode] = useState("");
  const [needsDisambiguationRef, setNeedsDisambiguationRef] = useState(false);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [info, setInfo] = useState("");

  useEffect(() => {
    if (!portalEnabled || !hasSessionCookie) return;
    let active = true;
    portalFetch<{ session: PortalSessionView }>("/api/public/portal/auth/session")
      .then((res) => {
        if (!active) return;
        if (res.session?.authenticated) {
          if (
            res.session.scope_type === "BOOKING" &&
            res.session.scoped_booking_reference
          ) {
            router.replace(
              `/portal/bookings/${encodeURIComponent(res.session.scoped_booking_reference)}`,
            );
          } else {
            router.replace("/portal/bookings");
          }
          return;
        }
        setCheckingSession(false);
      })
      .catch(() => {
        if (active) setCheckingSession(false);
      });
    return () => {
      active = false;
    };
  }, [hasSessionCookie, portalEnabled, router]);

  if (!portalEnabled) {
    return (
      <div className="card-panel">
        <h1 className="section-title">Guest Portal</h1>
        <p style={{ color: "var(--text-muted)" }}>
          Online Guest Portal sign-in is not currently enabled for this property. You can still
          retrieve direct booking confirmations using your booking reference and security token.
        </p>
        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem", marginTop: "1rem" }}>
          <Link href="/lookup" className="btn btn-primary">
            Find My Booking
          </Link>
          <Link href="/stays" className="btn btn-secondary">
            Search Availability
          </Link>
        </div>
      </div>
    );
  }

  if (checkingSession) {
    return (
      <div className="card-panel" aria-live="polite">
        <p style={{ margin: 0, color: "var(--text-muted)" }}>
          Checking your Guest Portal session…
        </p>
      </div>
    );
  }

  async function onRequestChallenge(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setInfo("");
    try {
      await portalFetch("/api/public/portal/auth/challenge", {
        method: "POST",
        body: {
          email: email.trim(),
          purpose: "LOGIN",
          booking_reference: bookingRef.trim(),
        },
      });
      setCodeSent(true);
      setInfo(
        "If an eligible booking profile matches that email address, a 6-digit verification code and sign-in link have been sent (valid for 15 minutes).",
      );
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to send verification code right now.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function onVerifyCode(e: FormEvent) {
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
          "Multiple guest profiles share this email address. Please enter one of your booking references below to confirm which profile belongs to you.",
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

  async function onBookingTokenLogin(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await portalFetch<{ session: PortalSessionView }>(
        "/api/public/portal/auth/booking-token",
        {
          method: "POST",
          body: {
            booking_reference: bookingRef.trim(),
            confirmation_token: confirmationToken.trim(),
          },
        },
      );
      const targetRef = res.session.scoped_booking_reference || bookingRef.trim();
      router.push(`/portal/bookings/${encodeURIComponent(targetRef)}`);
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Invalid booking reference or confirmation security token.",
      );
    } finally {
      setBusy(false);
    }
  }

  function switchMode(next: "account" | "booking_token") {
    setMode(next);
    setCodeSent(false);
    setCode("");
    setNeedsDisambiguationRef(false);
    setError("");
    setInfo("");
  }

  return (
    <div className="card-panel">
      <h1 className="section-title">Guest Portal Sign-In</h1>
      <p style={{ color: "var(--text-muted)", marginBottom: "1.25rem" }}>
        Manage your stays, update arrival details, message our reservations team, or manage
        your communication preferences using passwordless verification.
      </p>

      <div
        className="portal-subnav"
        role="tablist"
        aria-label="Portal verification method"
        style={{ marginBottom: "1.5rem" }}
      >
        <button
          type="button"
          role="tab"
          aria-selected={mode === "account"}
          className={`portal-tab ${mode === "account" ? "portal-tab-active" : ""}`}
          onClick={() => switchMode("account")}
        >
          Email Verification Code
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === "booking_token"}
          className={`portal-tab ${mode === "booking_token" ? "portal-tab-active" : ""}`}
          onClick={() => switchMode("booking_token")}
        >
          Single Booking Token
        </button>
      </div>

      {error ? (
        <div
          id="portal-auth-error"
          className="alert alert-err"
          role="alert"
          style={{ marginBottom: "1rem" }}
        >
          {error}
        </div>
      ) : null}
      {info ? (
        <div className="alert alert-ok" role="status" style={{ marginBottom: "1rem" }}>
          {info}
        </div>
      ) : null}

      {mode === "booking_token" ? (
        <form onSubmit={onBookingTokenLogin} style={{ display: "grid", gap: "1rem" }}>
          <div className="field">
            <label htmlFor="portal-token-ref">Booking Reference</label>
            <input
              id="portal-token-ref"
              type="text"
              required
              value={bookingRef}
              onChange={(e) => setBookingRef(e.target.value)}
              placeholder="e.g. TB-2026-AB12CD"
              aria-describedby={error ? "portal-auth-error" : undefined}
            />
          </div>
          <div className="field">
            <label htmlFor="portal-token-secret">Confirmation Security Token</label>
            <input
              id="portal-token-secret"
              type="password"
              autoComplete="off"
              required
              value={confirmationToken}
              onChange={(e) => setConfirmationToken(e.target.value)}
              placeholder="Paste the private token issued at checkout"
            />
          </div>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? "Verifying…" : "Access Single Booking"}
          </button>
        </form>
      ) : !codeSent ? (
        <form onSubmit={onRequestChallenge} style={{ display: "grid", gap: "1rem" }}>
          <div className="field">
            <label htmlFor="portal-email">Email Address Used for Booking</label>
            <input
              id="portal-email"
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
              aria-describedby={error ? "portal-auth-error" : undefined}
            />
          </div>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? "Sending Verification Code…" : "Send 6-Digit Verification Code"}
          </button>
        </form>
      ) : (
        <form onSubmit={onVerifyCode} style={{ display: "grid", gap: "1rem" }}>
          <div className="field">
            <label htmlFor="portal-verify-email">Email Address</label>
            <input
              id="portal-verify-email"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </div>
          {needsDisambiguationRef ? (
            <div className="field">
              <label htmlFor="portal-verify-ref">
                Booking Reference (Required to confirm your profile)
              </label>
              <input
                id="portal-verify-ref"
                type="text"
                required
                value={bookingRef}
                onChange={(e) => setBookingRef(e.target.value)}
                placeholder="e.g. TB-2026-AB12CD"
              />
            </div>
          ) : null}
          <div className="field">
            <label htmlFor="portal-verify-code">6-Digit Verification Code</label>
            <input
              id="portal-verify-code"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              required
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="123456"
              aria-describedby={error ? "portal-auth-error" : undefined}
            />
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem" }}>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? "Signing In…" : "Verify & Sign In"}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy}
              onClick={() => {
                setCodeSent(false);
                setCode("");
                setError("");
                setInfo("");
              }}
            >
              Send Another Code
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

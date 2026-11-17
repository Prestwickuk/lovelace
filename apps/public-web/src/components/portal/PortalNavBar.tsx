"use client";

import { useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { portalFetch, PortalClientError } from "@/lib/portal-client";
import type { PortalSessionView } from "@/lib/types";

export function PortalNavBar({
  session,
  activeSection,
}: {
  session: PortalSessionView;
  activeSection: "bookings" | "profile" | "privacy";
}) {
  const router = useRouter();
  const [signingOut, setSigningOut] = useState(false);

  async function onLogout() {
    setSigningOut(true);
    try {
      await portalFetch("/api/public/portal/auth/logout", {
        method: "POST",
        body: {},
        csrfToken: session.csrf_token,
      });
    } catch {
      // Ignore network errors on sign-out and navigate back to sign-in screen
    } finally {
      router.push("/portal");
      router.refresh();
    }
  }

  const isAccount = session.scope_type === "ACCOUNT";

  return (
    <div className="portal-header-bar">
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem", alignItems: "center" }}>
          <strong style={{ fontSize: "1.05rem", overflowWrap: "anywhere" }}>
            {isAccount && session.guest
              ? session.guest.full_name
              : `Booking ${session.scoped_booking_reference || ""}`}
          </strong>
          <span className="portal-badge">
            {isAccount ? "Verified Guest Account" : "Single-Booking Access"}
          </span>
          {session.guest?.is_do_not_contact ? (
            <span className="portal-badge portal-badge-warn">
              Do Not Contact Active
            </span>
          ) : null}
        </div>
        {isAccount && session.guest ? (
          <div
            style={{
              fontSize: "0.88rem",
              color: "var(--text-muted)",
              overflowWrap: "anywhere",
            }}
          >
            Signed in as {session.guest.verified_email}
          </div>
        ) : (
          <div style={{ fontSize: "0.88rem", color: "var(--text-muted)" }}>
            Scoped strictly to booking {session.scoped_booking_reference}. Sign in with your
            email on <Link href="/portal">the Guest Portal</Link> for full account &amp; privacy
            settings.
          </div>
        )}
      </div>

      <nav className="portal-subnav" aria-label="Guest portal sections">
        <Link
          href={
            !isAccount && session.scoped_booking_reference
              ? `/portal/bookings/${encodeURIComponent(session.scoped_booking_reference)}`
              : "/portal/bookings"
          }
          className="portal-tab"
          aria-current={activeSection === "bookings" ? "page" : undefined}
        >
          {isAccount ? "My Bookings" : "Booking Details"}
        </Link>
        {isAccount ? (
          <>
            <Link
              href="/portal/profile"
              className="portal-tab"
              aria-current={activeSection === "profile" ? "page" : undefined}
            >
              Profile &amp; Enquiries
            </Link>
            <Link
              href="/portal/privacy"
              className="portal-tab"
              aria-current={activeSection === "privacy" ? "page" : undefined}
            >
              Consent &amp; Privacy
            </Link>
          </>
        ) : null}
        <button
          type="button"
          onClick={onLogout}
          disabled={signingOut}
          className="btn btn-secondary"
          style={{ padding: "0.45rem 0.9rem", fontSize: "0.9rem" }}
        >
          {signingOut ? "Signing Out…" : "Sign Out"}
        </button>
      </nav>
    </div>
  );
}

export function StepUpVerifyBanner({
  session,
  onVerified,
}: {
  session: PortalSessionView;
  onVerified: (updated: PortalSessionView) => void;
}) {
  const [codeSent, setCodeSent] = useState(false);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function requestCode() {
    setBusy(true);
    setError("");
    try {
      await portalFetch("/api/public/portal/auth/step-up/challenge", {
        method: "POST",
        body: {},
        csrfToken: session.csrf_token,
      });
      setCodeSent(true);
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to send security code right now.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function confirmCode(e: FormEvent) {
    e.preventDefault();
    if (!code.trim()) return;
    setBusy(true);
    setError("");
    try {
      const res = await portalFetch<{ session: PortalSessionView }>(
        "/api/public/portal/auth/step-up/verify",
        {
          method: "POST",
          body: { code: code.trim() },
          csrfToken: session.csrf_token,
        },
      );
      setCode("");
      setCodeSent(false);
      onVerified(res.session);
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Invalid or expired security code.",
      );
    } finally {
      setBusy(false);
    }
  }

  if (session.step_up_fresh) {
    return null;
  }

  return (
    <div
      className="alert alert-warn"
      role="region"
      aria-label="Recent email verification required"
    >
      <strong>Recent Verification Required for Sensitive Requests</strong>
      <p style={{ margin: "0.35rem 0 0.75rem" }}>
        For your security, changing your primary email address or submitting a personal data
        access or erasure request requires confirming a fresh 6-digit email code within the last
        15 minutes.
      </p>
      {error ? (
        <div className="alert alert-err" role="alert" style={{ marginBottom: "0.75rem" }}>
          {error}
        </div>
      ) : null}
      {!codeSent ? (
        <button
          type="button"
          className="btn btn-primary"
          disabled={busy}
          onClick={requestCode}
        >
          {busy ? "Sending Code…" : "Send 6-Digit Security Code"}
        </button>
      ) : (
        <form
          onSubmit={confirmCode}
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "0.65rem",
            alignItems: "flex-end",
          }}
        >
          <div className="field" style={{ margin: 0 }}>
            <label htmlFor="stepup-code">6-Digit Security Code</label>
            <input
              id="stepup-code"
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
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? "Verifying…" : "Confirm Code"}
          </button>
        </form>
      )}
    </div>
  );
}

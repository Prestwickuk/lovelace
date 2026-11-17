"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { portalFetch, PortalClientError, statusBadgeClass } from "@/lib/portal-client";
import type {
  PortalPrivacyRequestReceipt,
  PortalProfileView,
  PortalSessionView,
} from "@/lib/types";
import { PortalNavBar, StepUpVerifyBanner } from "./PortalNavBar";

export function PortalPrivacyClient() {
  const router = useRouter();
  const [session, setSession] = useState<PortalSessionView | null>(null);
  const [profile, setProfile] = useState<PortalProfileView | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [info, setInfo] = useState("");

  const [updatingConsent, setUpdatingConsent] = useState(false);
  const [sarNotes, setSarNotes] = useState("");
  const [submittingSar, setSubmittingSar] = useState(false);
  const [erasureReason, setErasureReason] = useState("");
  const [confirmErasureAck, setConfirmErasureAck] = useState(false);
  const [submittingErasure, setSubmittingErasure] = useState(false);

  useEffect(() => {
    let active = true;
    async function load() {
      try {
        const sessRes = await portalFetch<{ session: PortalSessionView }>(
          "/api/public/portal/auth/session",
        );
        if (!active) return;
        if (sessRes.session.scope_type !== "ACCOUNT") {
          router.replace("/portal/bookings");
          return;
        }
        const profRes = await portalFetch<{ profile: PortalProfileView }>(
          "/api/public/portal/profile",
        );
        if (!active) return;
        setSession(sessRes.session);
        setProfile(profRes.profile);
      } catch (err) {
        if (!active) return;
        if (err instanceof PortalClientError && err.status === 401) {
          router.replace("/portal");
          return;
        }
        setError(
          err instanceof PortalClientError
            ? err.message
            : "Unable to load privacy settings.",
        );
      } finally {
        if (active) setLoading(false);
      }
    }
    load();
    return () => {
      active = false;
    };
  }, [router]);

  async function onUpdateConsent(status: "GRANTED" | "WITHDRAWN") {
    if (!session) return;
    setUpdatingConsent(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<{ profile: PortalProfileView }>(
        "/api/public/portal/consent",
        {
          method: "POST",
          body: { status },
          csrfToken: session.csrf_token,
        },
      );
      setProfile(res.profile);
      setInfo(
        status === "GRANTED"
          ? "Marketing email consent granted and recorded."
          : "Marketing email consent withdrawn. You will only receive essential transactional booking communications.",
      );
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to update marketing consent.",
      );
    } finally {
      setUpdatingConsent(false);
    }
  }

  async function onRequestSar(e: FormEvent) {
    e.preventDefault();
    if (!session) return;
    setSubmittingSar(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<PortalPrivacyRequestReceipt>(
        "/api/public/portal/privacy/sar",
        {
          method: "POST",
          body: { notes: sarNotes.trim() },
          csrfToken: session.csrf_token,
        },
      );
      setSarNotes("");
      setInfo(res.message);
    } catch (err) {
      if (err instanceof PortalClientError && err.code === "step_up_required") {
        setSession((prev) => (prev ? { ...prev, step_up_fresh: false } : prev));
      }
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to submit personal data access request.",
      );
    } finally {
      setSubmittingSar(false);
    }
  }

  async function onRequestErasure(e: FormEvent) {
    e.preventDefault();
    if (!session || !confirmErasureAck) return;
    setSubmittingErasure(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<PortalPrivacyRequestReceipt>(
        "/api/public/portal/privacy/erasure",
        {
          method: "POST",
          body: { reason: erasureReason.trim() },
          csrfToken: session.csrf_token,
        },
      );
      const profRes = await portalFetch<{ profile: PortalProfileView }>(
        "/api/public/portal/profile",
      );
      setProfile(profRes.profile);
      setErasureReason("");
      setConfirmErasureAck(false);
      setInfo(res.message);
    } catch (err) {
      if (err instanceof PortalClientError && err.code === "step_up_required") {
        setSession((prev) => (prev ? { ...prev, step_up_fresh: false } : prev));
      }
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to submit personal data erasure request.",
      );
    } finally {
      setSubmittingErasure(false);
    }
  }

  if (loading) {
    return (
      <div className="card-panel" aria-live="polite">
        <p style={{ margin: 0, color: "var(--text-muted)" }}>
          Loading privacy &amp; consent settings…
        </p>
      </div>
    );
  }

  if (!session || !profile) {
    return (
      <div className="card-panel">
        <div className="alert alert-err" role="alert">
          {error || "Privacy settings unavailable."}
        </div>
      </div>
    );
  }

  const optedIn = profile.marketing_consent_state === "GRANTED";

  return (
    <div>
      <PortalNavBar session={session} activeSection="privacy" />
      <h1 className="section-title">Consent &amp; Personal Data Requests</h1>

      {error ? (
        <div className="alert alert-err" role="alert" style={{ marginBottom: "1rem" }}>
          {error}
        </div>
      ) : null}
      {info ? (
        <div className="alert alert-ok" role="status" style={{ marginBottom: "1rem" }}>
          {info}
        </div>
      ) : null}

      <div style={{ marginBottom: "1.5rem" }}>
        <StepUpVerifyBanner session={session} onVerified={(next) => setSession(next)} />
      </div>

      <section className="card-panel" style={{ marginBottom: "1.75rem" }}>
        <h2>Marketing Email Consent</h2>
        <p style={{ color: "var(--text-muted)", fontSize: "0.94rem" }}>
          Current status:{" "}
          <span className={statusBadgeClass(profile.marketing_consent_state)}>
            {profile.marketing_consent_state}
          </span>
          . Essential booking confirmations, arrival instructions, and security codes are
          always governed separately from optional marketing updates.
        </p>

        {profile.is_do_not_contact ? (
          <div className="alert alert-warn">
            Marketing opt-in is disabled while Do-Not-Contact status is active on your
            profile.
          </div>
        ) : (
          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem" }}>
            <button
              type="button"
              className={optedIn ? "btn btn-secondary" : "btn btn-primary"}
              disabled={updatingConsent || optedIn}
              onClick={() => onUpdateConsent("GRANTED")}
            >
              Opt In to Seasonal Updates
            </button>
            <button
              type="button"
              className={!optedIn ? "btn btn-secondary" : "btn btn-danger-outline"}
              disabled={updatingConsent || profile.marketing_consent_state === "WITHDRAWN"}
              onClick={() => onUpdateConsent("WITHDRAWN")}
            >
              Withdraw Marketing Consent
            </button>
          </div>
        )}

        {profile.consent_history.length > 0 ? (
          <div
            style={{
              marginTop: "1.25rem",
              borderTop: "1px solid var(--border-subtle)",
              paddingTop: "0.85rem",
            }}
          >
            <strong style={{ fontSize: "0.9rem" }}>Recorded Consent Ledger</strong>
            <ul
              style={{
                margin: "0.5rem 0 0",
                paddingLeft: "1.25rem",
                fontSize: "0.88rem",
                color: "var(--text-muted)",
              }}
            >
              {profile.consent_history.slice(0, 8).map((entry, idx) => (
                <li key={`${entry.occurred_at}-${idx}`}>
                  <strong>{entry.status}</strong> via {entry.method} on{" "}
                  {new Date(entry.occurred_at).toLocaleString()}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </section>

      <div className="cards-grid-2">
        <section className="card-panel" style={{ minWidth: 0 }}>
          <h2>Request a Copy of Personal Data</h2>
          <p style={{ fontSize: "0.92rem", color: "var(--text-muted)" }}>
            Submit an authenticated personal data access request for review by our property
            team. No guest data is exported automatically from the portal; our team verifies
            identity and prepares responses directly.
          </p>
          <form onSubmit={onRequestSar} style={{ display: "grid", gap: "1rem" }}>
            <div className="field">
              <label htmlFor="sar-notes">Scope or Notes (optional)</label>
              <textarea
                id="sar-notes"
                rows={3}
                maxLength={500}
                value={sarNotes}
                onChange={(e) => setSarNotes(e.target.value)}
                placeholder="Optional context (e.g. relevant booking dates or correspondence)"
              />
            </div>
            <div>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={submittingSar || !session.step_up_fresh}
              >
                {submittingSar ? "Submitting…" : "Submit Data Access Request"}
              </button>
            </div>
          </form>
        </section>

        <section className="card-panel" style={{ minWidth: 0 }}>
          <h2>Request Personal Data Erasure</h2>
          <p style={{ fontSize: "0.92rem", color: "var(--text-muted)" }}>
            Submit an authenticated request to erase or anonymise your personal profile for
            review by our property team. Active stays, financial records, and disputes are
            reviewed by staff before any anonymisation is applied.
          </p>
          <form onSubmit={onRequestErasure} style={{ display: "grid", gap: "1rem" }}>
            <div className="field">
              <label htmlFor="erasure-reason">Reason (optional)</label>
              <textarea
                id="erasure-reason"
                rows={2}
                maxLength={500}
                value={erasureReason}
                onChange={(e) => setErasureReason(e.target.value)}
              />
            </div>
            <label
              style={{
                display: "flex",
                gap: "0.6rem",
                alignItems: "flex-start",
                fontSize: "0.9rem",
              }}
            >
              <input
                type="checkbox"
                checked={confirmErasureAck}
                onChange={(e) => setConfirmErasureAck(e.target.checked)}
                style={{ marginTop: "0.25rem" }}
              />
              <span>
                I understand that once personal data erasure is completed after staff review,
                Guest Portal access for historical bookings will be permanently removed.
              </span>
            </label>
            <div>
              <button
                type="submit"
                className="btn btn-danger-outline"
                disabled={submittingErasure || !confirmErasureAck || !session.step_up_fresh}
              >
                {submittingErasure ? "Submitting…" : "Submit Erasure Request"}
              </button>
            </div>
          </form>
        </section>
      </div>
    </div>
  );
}

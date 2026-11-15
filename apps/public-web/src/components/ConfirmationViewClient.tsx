"use client";

import { useCallback, useEffect, useState, type FormEvent } from "react";
import Link from "next/link";
import { formatMoney, formatStayDate } from "@/lib/format";
import type { PublicConfirmationView } from "@/lib/types";

export function ConfirmationViewClient({
  initialRef,
}: {
  initialRef: string;
}) {
  const [token, setToken] = useState("");
  const [manualToken, setManualToken] = useState("");
  const [view, setView] = useState<PublicConfirmationView | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [needsTokenPrompt, setNeedsTokenPrompt] = useState(false);

  const [cancelNote, setCancelNote] = useState("");
  const [cancelBusy, setCancelBusy] = useState(false);
  const [cancelSuccess, setCancelSuccess] = useState(false);
  const [rotateBusy, setRotateBusy] = useState(false);
  const [rotatedTokenMsg, setRotatedTokenMsg] = useState("");

  const lookupConfirmation = useCallback(
    async function runLookup(refToLookup: string, activeToken: string, attempt = 0): Promise<void> {
      const res = await fetch("/api/public/booking/confirmation", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${activeToken}`,
        },
        cache: "no-store",
        body: JSON.stringify({
          booking_reference: refToLookup,
          token: activeToken,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(
          data.error?.message ||
            "We could not verify that booking reference and access token.",
        );
        setLoading(false);
        return;
      }
      const conf = data as PublicConfirmationView;
      if (conf.status.startsWith("PENDING_PAYMENT") && attempt < 4) {
        await new Promise((r) => setTimeout(r, 700));
        return runLookup(refToLookup, activeToken, attempt + 1);
      }
      try {
        if (conf.reservation_reference) {
          sessionStorage.setItem(`tb_token_${conf.reservation_reference}`, activeToken);
        }
        if (conf.hold_reference) {
          sessionStorage.setItem(`tb_token_${conf.hold_reference}`, activeToken);
        }
      } catch {
        // Ignore storage failure
      }
      setNeedsTokenPrompt(false);
      setView(conf);
      setLoading(false);
    },
    [],
  );

  useEffect(() => {
    let cancelled = false;

    async function hydrateConfirmation() {
      let activeToken = "";
      if (typeof window !== "undefined") {
        // Strip any legacy ?token= parameter from the browser address bar immediately.
        const url = new URL(window.location.href);
        const legacyToken = url.searchParams.get("token") || "";
        if (legacyToken) {
          activeToken = legacyToken;
          url.searchParams.delete("token");
          window.history.replaceState(null, "", url.pathname + url.search);
        }
        if (initialRef) {
          try {
            if (activeToken) {
              sessionStorage.setItem(`tb_token_${initialRef}`, activeToken);
              sessionStorage.setItem(`tb_token_${initialRef.toUpperCase()}`, activeToken);
            } else {
              activeToken =
                sessionStorage.getItem(`tb_token_${initialRef}`) ||
                sessionStorage.getItem(`tb_token_${initialRef.toUpperCase()}`) ||
                "";
            }
          } catch {
            // Ignore storage failure
          }
        }
      }

      await Promise.resolve();
      if (cancelled) return;

      if (activeToken) setToken(activeToken);

      if (!initialRef || !activeToken) {
        setLoading(false);
        setNeedsTokenPrompt(true);
        setError(
          "Please enter both your booking reference and security access token to view booking details.",
        );
        return;
      }

      try {
        await lookupConfirmation(initialRef, activeToken, 0);
      } catch {
        if (!cancelled) {
          setError("Unable to retrieve confirmation details right now.");
          setLoading(false);
        }
      }
    }

    void hydrateConfirmation();
    return () => {
      cancelled = true;
    };
  }, [initialRef, lookupConfirmation]);

  async function handleManualTokenSubmit(e: FormEvent) {
    e.preventDefault();
    const cleaned = manualToken.trim();
    if (!initialRef || !cleaned) return;
    setLoading(true);
    setError("");
    setToken(cleaned);
    try {
      sessionStorage.setItem(`tb_token_${initialRef}`, cleaned);
    } catch {
      // Ignore storage unavailability
    }
    await lookupConfirmation(initialRef, cleaned, 0);
  }

  async function handleRotateToken() {
    if (!view || !token) return;
    setRotateBusy(true);
    setError("");
    setRotatedTokenMsg("");
    const refToUse = view.reservation_reference || view.hold_reference || initialRef;
    try {
      const res = await fetch("/api/public/booking/rotate-token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ booking_reference: refToUse, token }),
      });
      const data = await res.json();
      if (!res.ok || !data.confirmation_token) {
        setError(data.error?.message || "Could not rotate confirmation token.");
        return;
      }
      const nextTok = data.confirmation_token as string;
      setToken(nextTok);
      try {
        sessionStorage.setItem(`tb_token_${refToUse}`, nextTok);
        if (view.hold_reference) sessionStorage.setItem(`tb_token_${view.hold_reference}`, nextTok);
        if (view.reservation_reference) sessionStorage.setItem(`tb_token_${view.reservation_reference}`, nextTok);
      } catch {
        // Ignore storage failure
      }
      setRotatedTokenMsg(`New security token issued (previous token revoked): ${nextTok}`);
    } catch {
      setError("Network error rotating token.");
    } finally {
      setRotateBusy(false);
    }
  }

  async function handleCancelRequest(e: FormEvent) {
    e.preventDefault();
    if (!view) return;
    setCancelBusy(true);
    setError("");

    const refToUse = view.reservation_reference || view.hold_reference || initialRef;
    try {
      const res = await fetch("/api/public/booking/cancel-request", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          booking_reference: refToUse,
          token,
          note: cancelNote,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.error?.message || "Could not submit cancellation request.");
        return;
      }
      setView(data as PublicConfirmationView);
      setCancelSuccess(true);
    } catch {
      setError("Unable to reach booking server.");
    } finally {
      setCancelBusy(false);
    }
  }

  if (loading) {
    return (
      <div className="card-panel" role="status">
        Confirming payment status with Tir Bach Campsite...
      </div>
    );
  }

  if (error && !view) {
    return (
      <div className="card-panel" style={{ maxWidth: "560px" }}>
        <div className="alert alert-err" role="alert">
          {error}
        </div>
        {needsTokenPrompt && initialRef ? (
          <form onSubmit={handleManualTokenSubmit} style={{ display: "grid", gap: "0.85rem", marginBottom: "1rem" }}>
            <div className="field">
              <label htmlFor="confirm-token-input">
                Enter security token for reference <code>{initialRef}</code>
              </label>
              <input
                id="confirm-token-input"
                type="password"
                autoComplete="off"
                required
                value={manualToken}
                onChange={(e) => setManualToken(e.target.value)}
                placeholder="Paste your booking security token"
              />
            </div>
            <button type="submit" className="btn btn-primary" style={{ width: "fit-content" }}>
              Verify &amp; View Booking
            </button>
          </form>
        ) : null}
        <Link href="/lookup" className="btn btn-secondary">
          Open Booking Lookup
        </Link>
      </div>
    );
  }

  if (!view) return null;

  const isConfirmed =
    view.status === "CONFIRMED" || view.status === "CHECKED_IN";
  const isCancelled = view.status === "CANCELLED";
  const badgeClass = isConfirmed
    ? "badge-ok"
    : isCancelled
      ? "badge-err"
      : "badge-warn";

  return (
    <div style={{ display: "grid", gap: "1.5rem" }}>
      {error && (
        <div className="alert alert-err" role="alert">
          {error}
        </div>
      )}

      {cancelSuccess && (
        <div className="alert alert-info" role="status">
          Your cancellation request has been logged for our team and marked on your booking record.
        </div>
      )}

      <section className="card-panel">
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "flex-start",
            flexWrap: "wrap",
            gap: "1rem",
          }}
        >
          <div>
            <span className={`badge ${badgeClass}`}>Status: {view.status}</span>{" "}
            <span className="badge">Payment: {view.payment_status}</span>
            <h1 className="section-title" style={{ marginTop: "0.65rem" }}>
              {isConfirmed
                ? "Your Pembrokeshire Stay is Confirmed"
                : isCancelled
                  ? "Booking Cancelled"
                  : "Booking & Payment Status"}
            </h1>
            <p style={{ margin: 0, color: "var(--text-muted)" }}>
              Reference:{" "}
              <strong style={{ fontFamily: "monospace", fontSize: "1.05rem" }}>
                {view.reservation_reference || view.hold_reference}
              </strong>
              {view.reservation_reference && view.hold_reference
                ? ` (from hold ${view.hold_reference})`
                : ""}
            </p>
          </div>

          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => window.print()}
          >
            Print Confirmation
          </button>
        </div>

        <div
          className="cards-grid-2"
          style={{ marginTop: "1.5rem", borderTop: "1px solid var(--border-subtle)", paddingTop: "1.25rem" }}
        >
          <div>
            <h2 style={{ fontSize: "1.15rem", color: "var(--brand-forest)" }}>
              Stay Details
            </h2>
            <p style={{ color: "var(--text-muted)", marginBottom: 0 }}>
              <strong>Property:</strong> {view.property_name}
              <br />
              <strong>Accommodation:</strong> {view.unit_type_name}
              <br />
              <strong>Arrival:</strong> {formatStayDate(view.arrival_date)} (Check-in{" "}
              {view.check_in_time}+)
              <br />
              <strong>Departure:</strong> {formatStayDate(view.departure_date)} (By{" "}
              {view.check_out_time})
              <br />
              <strong>Party:</strong> {view.adults} adults
              {view.children > 0 ? `, ${view.children} children` : ""}
              {view.infants > 0 ? `, ${view.infants} infants` : ""}
              <br />
              <strong>Lead Guest:</strong> {view.guest_name} ({view.guest_email})
            </p>
          </div>

          {view.quote && (
            <div>
              <h2 style={{ fontSize: "1.15rem", color: "var(--brand-forest)" }}>
                Authoritative Price Breakdown
              </h2>
              <div className="summary-row">
                <span>Accommodation ({view.nights} nights)</span>
                <span>
                  {formatMoney(
                    view.quote.accommodation_subtotal_pence,
                    view.quote.currency,
                  )}
                </span>
              </div>
              {view.quote.occupancy_surcharge_pence > 0 && (
                <div className="summary-row">
                  <span>Extra guests</span>
                  <span>
                    {formatMoney(
                      view.quote.occupancy_surcharge_pence,
                      view.quote.currency,
                    )}
                  </span>
                </div>
              )}
              {view.quote.extras_breakdown?.map((line) => (
                <div key={line.extra_id} className="summary-row">
                  <span>
                    {line.name} × {line.quantity}
                  </span>
                  <span>{formatMoney(line.total_pence, view.quote!.currency)}</span>
                </div>
              ))}
              {view.quote.tax_pence > 0 && (
                <div className="summary-row">
                  <span>VAT / Taxes</span>
                  <span>{formatMoney(view.quote.tax_pence, view.quote.currency)}</span>
                </div>
              )}
              <div className="summary-row summary-total">
                <span>Total Booking Value</span>
                <span>{formatMoney(view.quote.total_pence, view.quote.currency)}</span>
              </div>
              <div className="summary-row" style={{ color: "var(--brand-forest)", fontWeight: 600 }}>
                <span>Deposit Due / Charged</span>
                <span>
                  {formatMoney(view.quote.deposit_due_pence, view.quote.currency)}
                </span>
              </div>
              {view.quote.balance_due_pence > 0 && (
                <div className="summary-row" style={{ color: "var(--text-muted)" }}>
                  <span>
                    Remaining Balance
                    {view.quote.balance_due_date
                      ? ` (due ${view.quote.balance_due_date})`
                      : ""}
                  </span>
                  <span>
                    {formatMoney(view.quote.balance_due_pence, view.quote.currency)}
                  </span>
                </div>
              )}
            </div>
          )}
        </div>
      </section>

      <section className="card-panel">
        <h2 style={{ fontSize: "1.15rem", color: "var(--brand-forest)" }}>
          Cancellation &amp; Guest Support
        </h2>
        <p style={{ color: "var(--text-muted)", fontSize: "0.94rem" }}>
          {view.cancellation_policy}
          <br />
          For direct help, contact {view.contact_email || "the property team"}
          {view.contact_phone ? ` or ${view.contact_phone}` : ""}.
        </p>

        {rotatedTokenMsg && (
          <div className="alert alert-info" role="status">
            {rotatedTokenMsg}
          </div>
        )}

        <div style={{ marginBottom: "1rem" }}>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={rotateBusy}
            onClick={handleRotateToken}
          >
            {rotateBusy ? "Rotating Token..." : "Rotate Access Token"}
          </button>
        </div>

        {view.cancellation_requested ? (
          <div className="alert alert-warn">
            A cancellation request has already been submitted for this reservation.
          </div>
        ) : (
          !isCancelled && (
            <form onSubmit={handleCancelRequest} style={{ display: "grid", gap: "0.85rem", maxWidth: "520px" }}>
              <div className="field">
                <label htmlFor="cancel_note">
                  Need to cancel? Request cancellation with optional note
                </label>
                <textarea
                  id="cancel_note"
                  value={cancelNote}
                  onChange={(e) => setCancelNote(e.target.value)}
                  placeholder="Optional reason or message for the Tir Bach hosts..."
                />
              </div>
              <button
                type="submit"
                className="btn btn-danger-outline"
                disabled={cancelBusy}
                style={{ width: "fit-content" }}
              >
                {cancelBusy ? "Submitting request..." : "Request Cancellation"}
              </button>
            </form>
          )
        )}
      </section>
    </div>
  );
}

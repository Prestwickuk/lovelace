"use client";

import { useEffect, useMemo, useState, type FormEvent } from "react";
import Link from "next/link";
import {
  formatMoney,
  formatPricingModel,
  formatStayDate,
  isSafeCheckoutRedirectUrl,
} from "@/lib/format";
import type {
  BookingHold,
  BookingQuote,
  PublicExtraView,
  PublicPropertyView,
  PublicUnitTypeView,
  QuoteExtraSelection,
} from "@/lib/types";

function formatRemaining(ms: number): string {
  if (ms <= 0) return "00:00";
  const totalSec = Math.floor(ms / 1000);
  const mins = Math.floor(totalSec / 60);
  const secs = totalSec % 60;
  return `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
}

export function BookingWizard({
  property,
  unitType,
  extras,
  initialQuote,
  arrival,
  departure,
  adults,
  childrenCount,
  infants,
  cancelledReturn,
}: {
  property: PublicPropertyView;
  unitType: PublicUnitTypeView;
  extras: PublicExtraView[];
  initialQuote: BookingQuote;
  arrival: string;
  departure: string;
  adults: number;
  childrenCount: number;
  infants: number;
  cancelledReturn?: boolean;
}) {
  const [quantities, setQuantities] = useState<Record<string, number>>({});
  const [quote, setQuote] = useState<BookingQuote>(initialQuote);
  const [pricingLoading, setPricingLoading] = useState(false);

  const [guestName, setGuestName] = useState("");
  const [guestEmail, setGuestEmail] = useState("");
  const [guestPhone, setGuestPhone] = useState("");
  const [specialRequests, setSpecialRequests] = useState("");
  const [acceptTerms, setAcceptTerms] = useState(false);

  const [submittingHold, setSubmittingHold] = useState(false);
  const stripeEnabled = Boolean(property.policy.stripe_enabled);
  const [initiatingCheckout, setInitiatingCheckout] = useState(false);
  const [releasingHold, setReleasingHold] = useState(false);

  const [hold, setHold] = useState<BookingHold | null>(null);
  const [confirmationToken, setConfirmationToken] = useState("");
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [errorMsg, setErrorMsg] = useState(
    cancelledReturn
      ? "Checkout was cancelled before payment completed. Your dates remain available to retry or modify."
      : "",
  );
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const selectedExtras: QuoteExtraSelection[] = useMemo(
    () =>
      Object.entries(quantities)
        .filter(([, q]) => q > 0)
        .map(([extra_id, quantity]) => ({ extra_id, quantity })),
    [quantities],
  );

  // Live countdown timer while hold is active
  useEffect(() => {
    if (!hold || hold.status !== "ACTIVE") return;
    const timer = setInterval(() => setNowMs(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [hold]);

  const holdRemainingMs = hold
    ? Math.max(0, new Date(hold.expires_at).getTime() - nowMs)
    : 0;
  const holdExpired = Boolean(hold && hold.status === "ACTIVE" && holdRemainingMs === 0);

  async function updateExtraQty(extraId: string, nextQty: number) {
    if (hold) return; // Snapshot locked once hold is created
    const updated = { ...quantities, [extraId]: Math.max(0, nextQty) };
    setQuantities(updated);
    setPricingLoading(true);
    setErrorMsg("");

    const extrasList: QuoteExtraSelection[] = Object.entries(updated)
      .filter(([, q]) => q > 0)
      .map(([extra_id, quantity]) => ({ extra_id, quantity }));

    try {
      const res = await fetch("/api/public/pricing", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          property_slug: property.slug,
          unit_type_code: unitType.code,
          arrival_date: arrival,
          departure_date: departure,
          adults,
          children: childrenCount,
          infants,
          extras: extrasList,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setErrorMsg(data.error?.message || "Could not recalculate pricing.");
      } else if (data.quote) {
        setQuote(data.quote);
      }
    } catch {
      setErrorMsg("Unable to update pricing right now.");
    } finally {
      setPricingLoading(false);
    }
  }

  async function handleCreateHold(e: FormEvent) {
    e.preventDefault();
    setSubmittingHold(true);
    setErrorMsg("");
    setFieldErrors({});

    try {
      const res = await fetch("/api/public/booking/holds", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          property_slug: property.slug,
          unit_type_code: unitType.code,
          arrival_date: arrival,
          departure_date: departure,
          adults,
          children: childrenCount,
          infants,
          extras: selectedExtras,
          guest_name: guestName,
          guest_email: guestEmail,
          guest_phone: guestPhone,
          special_requests: specialRequests,
          accept_terms: acceptTerms,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setErrorMsg(data.error?.message || "Unable to reserve these dates.");
        if (data.error?.fields) {
          setFieldErrors(data.error.fields);
        }
        return;
      }

      setHold(data.hold);
      setQuote(data.quote);
      setConfirmationToken(data.confirmation_token || "");
      try {
        if (data.hold?.hold_reference && data.confirmation_token) {
          sessionStorage.setItem(
            `tb_token_${data.hold.hold_reference}`,
            data.confirmation_token,
          );
        }
      } catch {
        // Ignore storage unavailability
      }
    } catch {
      setErrorMsg("Network error while creating booking hold. Please retry.");
    } finally {
      setSubmittingHold(false);
    }
  }

  async function handleProceedToCheckout() {
    if (!hold) return;
    setInitiatingCheckout(true);
    setErrorMsg("");

    try {
      const res = await fetch("/api/public/booking/checkout", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          hold_id: hold.id,
          idempotency_key: `web-co-${hold.id}`,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setErrorMsg(data.error?.message || "Could not start payment checkout.");
        return;
      }
      if (typeof data.checkout_url === "string" && data.checkout_url.trim() !== "") {
        const target = data.checkout_url.trim();
        if (!isSafeCheckoutRedirectUrl(target, window.location.origin)) {
          setErrorMsg("Payment gateway returned an invalid checkout redirect URL.");
          return;
        }
        window.location.href = target;
      } else {
        setErrorMsg("Payment gateway did not return a checkout URL.");
      }
    } catch {
      setErrorMsg("Unable to contact payment checkout service.");
    } finally {
      setInitiatingCheckout(false);
    }
  }

  async function handleReleaseHold() {
    if (!hold) return;
    setReleasingHold(true);
    setErrorMsg("");
    try {
      await fetch(`/api/public/booking/holds/${encodeURIComponent(hold.id)}/cancel`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      setHold(null);
      setConfirmationToken("");
    } catch {
      setErrorMsg("Could not release hold right now.");
    } finally {
      setReleasingHold(false);
    }
  }

  return (
    <div className="two-col-booking">
      <div>
        {!stripeEnabled && (
          <div className="alert alert-warn" role="status">
            <strong>Online Card Checkout Unavailable:</strong> Live online Stripe
            payments are currently disabled in property settings. Please contact{" "}
            {property.contact_email || "the property team"}
            {property.contact_phone ? ` (${property.contact_phone})` : ""} to arrange
            a booking directly.
          </div>
        )}

        {errorMsg && (
          <div className="alert alert-err" role="alert">
            {errorMsg}
          </div>
        )}

        {hold ? (
          <section className="card-panel" aria-live="polite">
            <span className="badge badge-ok">Dates Temporarily Reserved</span>
            <h2 style={{ marginTop: "0.7rem", color: "var(--brand-forest)" }}>
              Hold Reference: <span style={{ fontFamily: "monospace" }}>{hold.hold_reference}</span>
            </h2>

            {holdExpired ? (
              <div className="alert alert-err">
                Your temporary inventory hold has expired. Please release this hold and search dates again.
              </div>
            ) : (
              <div className="alert alert-info">
                <strong>Inventory Held for {formatRemaining(holdRemainingMs)}</strong>
                <br />
                Your {unitType.name} is exclusively held while you complete secure payment.
                {confirmationToken && (
                  <>
                    <br />
                    <small>
                      Save your lookup token in case you close this window:{" "}
                      <code>{confirmationToken}</code>
                    </small>
                  </>
                )}
              </div>
            )}

            <p style={{ color: "var(--text-muted)", fontSize: "0.95rem" }}>
              Guest: <strong>{hold.guest_name}</strong> ({hold.guest_email})
              <br />
              Amount due now via Stripe Checkout:{" "}
              <strong>{formatMoney(quote.deposit_due_pence, quote.currency)}</strong>
            </p>

            <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap", marginTop: "1rem" }}>
              {!holdExpired && stripeEnabled && (
                <button
                  type="button"
                  className="btn btn-accent"
                  disabled={initiatingCheckout}
                  onClick={handleProceedToCheckout}
                >
                  {initiatingCheckout
                    ? "Connecting to Stripe..."
                    : `Pay ${formatMoney(quote.deposit_due_pence, quote.currency)} Securely`}
                </button>
              )}
              {!holdExpired && !stripeEnabled && (
                <span className="badge badge-warn">
                  Online Stripe Checkout Disabled by Property Policy
                </span>
              )}

              <button
                type="button"
                className="btn btn-secondary"
                disabled={releasingHold || initiatingCheckout}
                onClick={handleReleaseHold}
              >
                {releasingHold ? "Releasing..." : "Release Hold & Edit Details"}
              </button>
            </div>
          </section>
        ) : (
          <form onSubmit={handleCreateHold} style={{ display: "grid", gap: "1.5rem" }}>
            {extras.length > 0 && (
              <section className="card-panel">
                <h2 style={{ color: "var(--brand-forest)", fontSize: "1.25rem" }}>
                  1. Optional Campsite Extras
                </h2>
                <div style={{ display: "grid", gap: "0.9rem" }}>
                  {extras.map((ex) => {
                    const qty = quantities[ex.id] || 0;
                    return (
                      <div
                        key={ex.id}
                        style={{
                          display: "flex",
                          justifyContent: "space-between",
                          alignItems: "center",
                          gap: "1rem",
                          paddingBottom: "0.75rem",
                          borderBottom: "1px solid var(--border-subtle)",
                        }}
                      >
                        <div>
                          <strong>{ex.name}</strong>
                          <div style={{ fontSize: "0.86rem", color: "var(--text-muted)" }}>
                            {ex.description} ·{" "}
                            <strong>{formatMoney(ex.price_pence, property.currency)}</strong>{" "}
                            {formatPricingModel(ex.pricing_model)}
                          </div>
                        </div>

                        <div className="field" style={{ minWidth: "96px" }}>
                          <label htmlFor={`extra-${ex.id}`}>Qty</label>
                          <select
                            id={`extra-${ex.id}`}
                            value={qty}
                            onChange={(e) =>
                              updateExtraQty(ex.id, Number(e.target.value))
                            }
                          >
                            {Array.from({ length: ex.max_quantity + 1 }, (_, i) => i).map(
                              (n) => (
                                <option key={n} value={n}>
                                  {n}
                                </option>
                              ),
                            )}
                          </select>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </section>
            )}

            <section className="card-panel">
              <h2 style={{ color: "var(--brand-forest)", fontSize: "1.25rem" }}>
                {extras.length > 0 ? "2. Guest Details" : "1. Guest Details"}
              </h2>

              <div style={{ display: "grid", gap: "1rem" }}>
                <div className="field">
                  <label htmlFor="guest_name">Full name</label>
                  <input
                    id="guest_name"
                    name="guest_name"
                    type="text"
                    required
                    value={guestName}
                    onChange={(e) => setGuestName(e.target.value)}
                    placeholder="e.g. Nia Davies"
                  />
                  {fieldErrors.guest_name && (
                    <small style={{ color: "var(--status-err-fg)" }}>
                      {fieldErrors.guest_name}
                    </small>
                  )}
                </div>

                <div className="field">
                  <label htmlFor="guest_email">Email address (for confirmation &amp; arrival info)</label>
                  <input
                    id="guest_email"
                    name="guest_email"
                    type="email"
                    required
                    value={guestEmail}
                    onChange={(e) => setGuestEmail(e.target.value)}
                    placeholder="nia@example.wales"
                  />
                  {fieldErrors.guest_email && (
                    <small style={{ color: "var(--status-err-fg)" }}>
                      {fieldErrors.guest_email}
                    </small>
                  )}
                </div>

                <div className="field">
                  <label htmlFor="guest_phone">Mobile / telephone number</label>
                  <input
                    id="guest_phone"
                    name="guest_phone"
                    type="tel"
                    required
                    value={guestPhone}
                    onChange={(e) => setGuestPhone(e.target.value)}
                    placeholder="+44 7700 900123"
                  />
                  {fieldErrors.guest_phone && (
                    <small style={{ color: "var(--status-err-fg)" }}>
                      {fieldErrors.guest_phone}
                    </small>
                  )}
                </div>

                <div className="field">
                  <label htmlFor="special_requests">
                    Arrival notes or special requests (optional)
                  </label>
                  <textarea
                    id="special_requests"
                    name="special_requests"
                    value={specialRequests}
                    onChange={(e) => setSpecialRequests(e.target.value)}
                    placeholder="Bringing a dog, late arrival estimate, accessibility needs..."
                  />
                </div>

                <label
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    gap: "0.6rem",
                    fontSize: "0.92rem",
                    cursor: "pointer",
                  }}
                >
                  <input
                    id="accept_terms"
                    name="accept_terms"
                    type="checkbox"
                    checked={acceptTerms}
                    onChange={(e) => setAcceptTerms(e.target.checked)}
                    style={{ marginTop: "0.25rem", width: "auto" }}
                    required
                  />
                  <span>
                    I agree to Tir Bach Campsite&apos;s{" "}
                    <Link href="/terms" target="_blank" rel="noopener noreferrer" style={{ fontWeight: 600 }}>
                      Booking Terms &amp; Cancellation Policy
                    </Link>{" "}
                    and{" "}
                    <Link href="/privacy" target="_blank" rel="noopener noreferrer" style={{ fontWeight: 600 }}>
                      Privacy Notice
                    </Link>
                    .
                  </span>
                </label>
                {fieldErrors.accept_terms && (
                  <small style={{ color: "var(--status-err-fg)" }}>
                    {fieldErrors.accept_terms}
                  </small>
                )}

                <button
                  type="submit"
                  className="btn btn-accent"
                  disabled={submittingHold || pricingLoading}
                >
                  {submittingHold
                    ? "Checking availability & holding dates..."
                    : "Hold Dates & Review Payment"}
                </button>
              </div>
            </section>
          </form>
        )}
      </div>

      <aside className="card-panel" aria-label="Booking price summary">
        <span className="badge">{unitType.name}</span>
        <h2 style={{ marginTop: "0.6rem", fontSize: "1.3rem", color: "var(--brand-forest)" }}>
          Stay Summary
        </h2>

        <p style={{ fontSize: "0.92rem", color: "var(--text-muted)", marginBottom: "1rem" }}>
          <strong>Check-in:</strong> {formatStayDate(quote.start_date)} ({property.check_in_time}+)
          <br />
          <strong>Check-out:</strong> {formatStayDate(quote.end_date)} (by {property.check_out_time})
          <br />
          <strong>Duration:</strong> {quote.nights}{" "}
          {quote.nights === 1 ? "night" : "nights"}
          <br />
          <strong>Party:</strong> {quote.adults} adults
          {quote.children > 0 ? `, ${quote.children} children` : ""}
          {quote.infants > 0 ? `, ${quote.infants} infants` : ""}
        </p>

        <div style={{ borderTop: "1px solid var(--border-subtle)", paddingTop: "0.65rem" }}>
          <div className="summary-row">
            <span>Accommodation ({quote.nights} nights)</span>
            <span>{formatMoney(quote.accommodation_subtotal_pence, quote.currency)}</span>
          </div>

          {quote.occupancy_surcharge_pence > 0 && (
            <div className="summary-row">
              <span>Extra guest charges</span>
              <span>{formatMoney(quote.occupancy_surcharge_pence, quote.currency)}</span>
            </div>
          )}

          {quote.extras_breakdown?.map((line) => (
            <div key={line.extra_id} className="summary-row">
              <span>
                {line.name} × {line.quantity}
              </span>
              <span>{formatMoney(line.total_pence, quote.currency)}</span>
            </div>
          ))}

          {quote.tax_pence > 0 && (
            <div className="summary-row">
              <span>VAT / Taxes</span>
              <span>{formatMoney(quote.tax_pence, quote.currency)}</span>
            </div>
          )}

          <div className="summary-row summary-total">
            <span>Total</span>
            <span>
              {pricingLoading
                ? "Updating..."
                : formatMoney(quote.total_pence, quote.currency)}
            </span>
          </div>

          <div className="summary-row" style={{ color: "var(--brand-forest)", fontWeight: 600 }}>
            <span>Due at Checkout ({quote.deposit_mode})</span>
            <span>{formatMoney(quote.deposit_due_pence, quote.currency)}</span>
          </div>

          {quote.balance_due_pence > 0 && (
            <div className="summary-row" style={{ color: "var(--text-muted)", fontSize: "0.86rem" }}>
              <span>
                Remaining Balance{quote.balance_due_date ? ` (due ${quote.balance_due_date})` : ""}
              </span>
              <span>{formatMoney(quote.balance_due_pence, quote.currency)}</span>
            </div>
          )}
        </div>

        <p style={{ fontSize: "0.82rem", color: "var(--text-muted)", marginTop: "1rem", marginBottom: 0 }}>
          {property.policy.cancellation_policy}
        </p>
      </aside>
    </div>
  );
}

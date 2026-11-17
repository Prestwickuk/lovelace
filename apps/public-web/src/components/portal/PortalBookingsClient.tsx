"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { formatStayDate, formatMoney } from "@/lib/format";
import { portalFetch, PortalClientError, statusBadgeClass } from "@/lib/portal-client";
import type { PortalBookingSummary, PortalSessionView } from "@/lib/types";
import { PortalNavBar } from "./PortalNavBar";

export function PortalBookingsClient() {
  const router = useRouter();
  const [session, setSession] = useState<PortalSessionView | null>(null);
  const [bookings, setBookings] = useState<PortalBookingSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    async function load() {
      try {
        const [sessRes, bookRes] = await Promise.all([
          portalFetch<{ session: PortalSessionView }>("/api/public/portal/auth/session"),
          portalFetch<{ bookings: PortalBookingSummary[] }>("/api/public/portal/bookings"),
        ]);
        if (!active) return;
        setSession(sessRes.session);
        setBookings(bookRes.bookings || []);
      } catch (err) {
        if (!active) return;
        if (err instanceof PortalClientError && err.status === 401) {
          router.replace("/portal");
          return;
        }
        setError(
          err instanceof PortalClientError
            ? err.message
            : "Unable to load your bookings right now.",
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

  if (loading) {
    return (
      <div className="card-panel" aria-live="polite">
        <p style={{ margin: 0, color: "var(--text-muted)" }}>Loading your bookings…</p>
      </div>
    );
  }

  if (error || !session) {
    return (
      <div className="card-panel">
        <div className="alert alert-err" role="alert">
          {error || "Session unavailable."}
        </div>
        <Link href="/portal" className="btn btn-primary" style={{ marginTop: "1rem" }}>
          Return to Portal Sign-In
        </Link>
      </div>
    );
  }

  return (
    <div>
      <PortalNavBar session={session} activeSection="bookings" />
      <h1 className="section-title">
        {session.scope_type === "ACCOUNT" ? "My Bookings" : "Your Scoped Booking"}
      </h1>

      {bookings.length === 0 ? (
        <div className="card-panel">
          <p style={{ marginTop: 0, color: "var(--text-muted)" }}>
            No bookings were found on your verified profile yet.
          </p>
          <Link href="/stays" className="btn btn-primary">
            Search Accommodation &amp; Availability
          </Link>
        </div>
      ) : (
        <div style={{ display: "grid", gap: "1rem" }}>
          {bookings.map((b) => (
            <article key={b.reference} className="portal-booking-card">
              <div
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  justifyContent: "space-between",
                  gap: "0.75rem",
                  alignItems: "center",
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <h2 style={{ margin: 0, fontSize: "1.25rem" }}>
                    Booking <code>{b.reference}</code>
                  </h2>
                  <div
                    style={{
                      fontSize: "0.95rem",
                      color: "var(--text-muted)",
                      marginTop: "0.2rem",
                    }}
                  >
                    {formatStayDate(b.arrival_date)} to {formatStayDate(b.departure_date)} ·{" "}
                    {b.nights} {b.nights === 1 ? "night" : "nights"} · {b.adults} adults
                    {b.children > 0 ? `, ${b.children} children` : ""}
                    {b.infants > 0 ? `, ${b.infants} infants` : ""}
                  </div>
                </div>
                <div
                  style={{
                    display: "flex",
                    flexWrap: "wrap",
                    gap: "0.45rem",
                    alignItems: "center",
                  }}
                >
                  <span className={statusBadgeClass(b.status)}>{b.status}</span>
                  {b.is_ota_managed ? (
                    <span className="portal-badge">Channel: {b.channel_code}</span>
                  ) : b.payment_status ? (
                    <span className={statusBadgeClass(b.payment_status)}>
                      Payment: {b.payment_status}
                    </span>
                  ) : null}
                  {b.cancellation_requested ? (
                    <span className="portal-badge portal-badge-warn">
                      Cancellation Requested
                    </span>
                  ) : null}
                </div>
              </div>

              {b.units.length > 0 ? (
                <div style={{ fontSize: "0.94rem" }}>
                  <strong>Accommodation:</strong>{" "}
                  {b.units.map((u) => `${u.unit_type_name} (${u.unit_name})`).join(", ")}
                </div>
              ) : null}

              {b.is_ota_managed ? (
                <div className="alert alert-warn" style={{ margin: 0 }}>
                  {b.billing_notice}
                </div>
              ) : b.total_pence !== undefined ? (
                <div style={{ fontSize: "0.94rem", color: "var(--text-muted)" }}>
                  Total: <strong>{formatMoney(b.total_pence, b.currency || "GBP")}</strong> ·
                  Paid:{" "}
                  <strong>
                    {formatMoney(b.amount_paid_pence || 0, b.currency || "GBP")}
                  </strong>
                  {b.balance_due_pence && b.balance_due_pence > 0 ? (
                    <>
                      {" "}
                      · Balance Due:{" "}
                      <strong>
                        {formatMoney(b.balance_due_pence, b.currency || "GBP")}
                      </strong>
                    </>
                  ) : null}
                </div>
              ) : null}

              <div
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  gap: "0.75rem",
                  alignItems: "center",
                }}
              >
                <Link
                  href={`/portal/bookings/${encodeURIComponent(b.reference)}`}
                  className="btn btn-primary"
                >
                  View Stay Details &amp; Manage
                </Link>
                {b.estimated_arrival_time ? (
                  <span style={{ fontSize: "0.88rem", color: "var(--text-muted)" }}>
                    ETA: <strong>{b.estimated_arrival_time}</strong>
                  </span>
                ) : null}
              </div>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

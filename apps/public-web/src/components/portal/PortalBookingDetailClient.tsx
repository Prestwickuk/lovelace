"use client";

import { useEffect, useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { formatStayDate, formatMoney } from "@/lib/format";
import { portalFetch, PortalClientError, statusBadgeClass } from "@/lib/portal-client";
import type {
  PortalBookingDetail,
  PortalEnquiryView,
  PortalSessionView,
} from "@/lib/types";
import { PortalNavBar } from "./PortalNavBar";

export function PortalBookingDetailClient({ reference }: { reference: string }) {
  const router = useRouter();
  const [session, setSession] = useState<PortalSessionView | null>(null);
  const [booking, setBooking] = useState<PortalBookingDetail | null>(null);
  const [enquiries, setEnquiries] = useState<PortalEnquiryView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [actionMsg, setActionMsg] = useState("");

  // Arrival details form
  const [eta, setEta] = useState("");
  const [arrivalNotes, setArrivalNotes] = useState("");
  const [savingArrival, setSavingArrival] = useState(false);

  // Change request form
  const [reqArrival, setReqArrival] = useState("");
  const [reqDeparture, setReqDeparture] = useState("");
  const [reqAdults, setReqAdults] = useState(2);
  const [reqChildren, setReqChildren] = useState(0);
  const [reqInfants, setReqInfants] = useState(0);
  const [changeNotes, setChangeNotes] = useState("");
  const [sendingChange, setSendingChange] = useState(false);

  // Cancellation request form
  const [cancelNote, setCancelNote] = useState("");
  const [sendingCancel, setSendingCancel] = useState(false);

  // Booking-linked question form
  const [questionSubject, setQuestionSubject] = useState("");
  const [questionBody, setQuestionBody] = useState("");
  const [sendingQuestion, setSendingQuestion] = useState(false);
  const [replyDrafts, setReplyDrafts] = useState<Record<string, string>>({});
  const [replyBusyId, setReplyBusyId] = useState("");

  useEffect(() => {
    let active = true;
    async function load() {
      try {
        const [sessRes, bookRes, enqRes] = await Promise.all([
          portalFetch<{ session: PortalSessionView }>("/api/public/portal/auth/session"),
          portalFetch<{ booking: PortalBookingDetail }>(
            `/api/public/portal/bookings/${encodeURIComponent(reference)}`,
          ),
          portalFetch<{ enquiries: PortalEnquiryView[] }>(
            "/api/public/portal/enquiries",
          ).catch(() => ({ enquiries: [] as PortalEnquiryView[] })),
        ]);
        if (!active) return;
        setSession(sessRes.session);
        setBooking(bookRes.booking);
        setEnquiries(
          (enqRes.enquiries || []).filter(
            (item) => item.reservation_reference === bookRes.booking.reference,
          ),
        );
        setEta(bookRes.booking.estimated_arrival_time || "");
        setArrivalNotes(bookRes.booking.guest_arrival_notes || "");
        setReqArrival(bookRes.booking.arrival_date);
        setReqDeparture(bookRes.booking.departure_date);
        setReqAdults(bookRes.booking.adults);
        setReqChildren(bookRes.booking.children);
        setReqInfants(bookRes.booking.infants);
      } catch (err) {
        if (!active) return;
        if (err instanceof PortalClientError && err.status === 401) {
          router.replace("/portal");
          return;
        }
        setError(
          err instanceof PortalClientError
            ? err.message
            : "Unable to load booking details.",
        );
      } finally {
        if (active) setLoading(false);
      }
    }
    load();
    return () => {
      active = false;
    };
  }, [reference, router]);

  async function onSaveArrival(e: FormEvent) {
    e.preventDefault();
    if (!session || !booking) return;
    setSavingArrival(true);
    setError("");
    setActionMsg("");
    try {
      const res = await portalFetch<{ booking: PortalBookingDetail }>(
        `/api/public/portal/bookings/${encodeURIComponent(booking.reference)}/arrival`,
        {
          method: "PATCH",
          body: {
            estimated_arrival_time: eta.trim(),
            guest_arrival_notes: arrivalNotes.trim(),
          },
          csrfToken: session.csrf_token,
        },
      );
      setBooking(res.booking);
      setActionMsg("Arrival details updated.");
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to save arrival details.",
      );
    } finally {
      setSavingArrival(false);
    }
  }

  async function onSubmitChangeRequest(e: FormEvent) {
    e.preventDefault();
    if (!session || !booking) return;
    setSendingChange(true);
    setError("");
    setActionMsg("");
    try {
      const res = await portalFetch<{ enquiry: PortalEnquiryView }>(
        `/api/public/portal/bookings/${encodeURIComponent(booking.reference)}/change-request`,
        {
          method: "POST",
          body: {
            requested_arrival: reqArrival,
            requested_departure: reqDeparture,
            requested_adults: reqAdults,
            requested_children: reqChildren,
            requested_infants: reqInfants,
            notes: changeNotes.trim(),
          },
          csrfToken: session.csrf_token,
        },
      );
      setEnquiries((prev) => [res.enquiry, ...prev]);
      setChangeNotes("");
      setActionMsg(
        `Booking change request submitted (${res.enquiry.reference}). Our reservations team will check availability and confirm any rate adjustment before updating your booking.`,
      );
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to submit change request.",
      );
    } finally {
      setSendingChange(false);
    }
  }

  async function onSubmitCancelRequest(e: FormEvent) {
    e.preventDefault();
    if (!session || !booking) return;
    setSendingCancel(true);
    setError("");
    setActionMsg("");
    try {
      const res = await portalFetch<{ booking: PortalBookingDetail }>(
        `/api/public/portal/bookings/${encodeURIComponent(booking.reference)}/cancel-request`,
        {
          method: "POST",
          body: { note: cancelNote.trim() },
          csrfToken: session.csrf_token,
        },
      );
      setBooking(res.booking);
      setCancelNote("");
      setActionMsg(
        "Cancellation request recorded for review by our reservations team.",
      );
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to submit cancellation request.",
      );
    } finally {
      setSendingCancel(false);
    }
  }

  async function onAskBookingQuestion(e: FormEvent) {
    e.preventDefault();
    if (!session || !booking) return;
    setSendingQuestion(true);
    setError("");
    setActionMsg("");
    try {
      const res = await portalFetch<{ enquiry: PortalEnquiryView }>(
        "/api/public/portal/enquiries",
        {
          method: "POST",
          body: {
            category: "BOOKING",
            booking_reference: booking.reference,
            subject: questionSubject.trim(),
            message: questionBody.trim(),
          },
          csrfToken: session.csrf_token,
        },
      );
      setEnquiries((prev) => [res.enquiry, ...prev]);
      setQuestionSubject("");
      setQuestionBody("");
      setActionMsg(`Message sent (${res.enquiry.reference}).`);
    } catch (err) {
      setError(
        err instanceof PortalClientError ? err.message : "Unable to send message.",
      );
    } finally {
      setSendingQuestion(false);
    }
  }

  async function onReplyBookingEnquiry(e: FormEvent, enquiryId: string) {
    e.preventDefault();
    if (!session) return;
    const draft = (replyDrafts[enquiryId] || "").trim();
    if (!draft) return;
    setReplyBusyId(enquiryId);
    setError("");
    try {
      const res = await portalFetch<{ enquiry: PortalEnquiryView }>(
        `/api/public/portal/enquiries/${encodeURIComponent(enquiryId)}/messages`,
        {
          method: "POST",
          body: { message: draft },
          csrfToken: session.csrf_token,
        },
      );
      setEnquiries((prev) =>
        prev.map((item) => (item.id === enquiryId ? res.enquiry : item)),
      );
      setReplyDrafts((prev) => ({ ...prev, [enquiryId]: "" }));
    } catch (err) {
      setError(err instanceof PortalClientError ? err.message : "Unable to send reply.");
    } finally {
      setReplyBusyId("");
    }
  }

  if (loading) {
    return (
      <div className="card-panel" aria-live="polite">
        <p style={{ margin: 0, color: "var(--text-muted)" }}>Loading stay details…</p>
      </div>
    );
  }

  if (!session || !booking) {
    return (
      <div className="card-panel">
        <div className="alert alert-err" role="alert">
          {error || "Booking could not be found on your session."}
        </div>
        <Link href="/portal/bookings" className="btn btn-primary" style={{ marginTop: "1rem" }}>
          Back to Bookings
        </Link>
      </div>
    );
  }

  const currency = booking.currency || "GBP";

  return (
    <div>
      <PortalNavBar session={session} activeSection="bookings" />

      {session.scope_type === "ACCOUNT" ? (
        <div style={{ marginBottom: "1rem" }}>
          <Link href="/portal/bookings" style={{ fontSize: "0.92rem", fontWeight: 600 }}>
            ← Back to My Bookings
          </Link>
        </div>
      ) : null}

      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          justifyContent: "space-between",
          gap: "0.75rem",
          alignItems: "center",
          marginBottom: "1.25rem",
        }}
      >
        <h1 className="section-title" style={{ margin: 0, overflowWrap: "anywhere" }}>
          Booking <code>{booking.reference}</code>
        </h1>
        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.45rem" }}>
          <span className={statusBadgeClass(booking.status)}>{booking.status}</span>
          {booking.is_ota_managed ? (
            <span className="portal-badge">Channel: {booking.channel_code}</span>
          ) : booking.payment_status ? (
            <span className={statusBadgeClass(booking.payment_status)}>
              Payment: {booking.payment_status}
            </span>
          ) : null}
          {booking.cancellation_requested ? (
            <span className="portal-badge portal-badge-warn">Cancellation Requested</span>
          ) : null}
        </div>
      </div>

      {error ? (
        <div className="alert alert-err" role="alert" style={{ marginBottom: "1rem" }}>
          {error}
        </div>
      ) : null}
      {actionMsg ? (
        <div className="alert alert-ok" role="status" style={{ marginBottom: "1rem" }}>
          {actionMsg}
        </div>
      ) : null}

      <div className="two-col-booking">
        <div style={{ display: "grid", gap: "1.5rem", minWidth: 0 }}>
          <section className="card-panel">
            <h2>Stay &amp; Arrival Summary</h2>
            <div className="summary-row">
              <span>Check-in</span>
              <strong>
                {formatStayDate(booking.arrival_date)} (from {booking.check_in_time})
              </strong>
            </div>
            <div className="summary-row">
              <span>Check-out</span>
              <strong>
                {formatStayDate(booking.departure_date)} (by {booking.check_out_time})
              </strong>
            </div>
            <div className="summary-row">
              <span>Duration &amp; Guests</span>
              <strong>
                {booking.nights} {booking.nights === 1 ? "night" : "nights"} ·{" "}
                {booking.adults} adults
                {booking.children > 0 ? `, ${booking.children} children` : ""}
                {booking.infants > 0 ? `, ${booking.infants} infants` : ""}
              </strong>
            </div>
            {booking.units.length > 0 ? (
              <div className="summary-row">
                <span>Accommodation</span>
                <strong>
                  {booking.units
                    .map((u) => `${u.unit_type_name} (${u.unit_name})`)
                    .join(", ")}
                </strong>
              </div>
            ) : null}
            {booking.estimated_arrival_time ? (
              <div className="summary-row">
                <span>Estimated Arrival</span>
                <strong>{booking.estimated_arrival_time}</strong>
              </div>
            ) : null}
            {booking.guest_arrival_notes ? (
              <p
                style={{
                  marginBottom: 0,
                  fontSize: "0.9rem",
                  color: "var(--text-muted)",
                  overflowWrap: "anywhere",
                }}
              >
                <strong>Arrival Notes:</strong> {booking.guest_arrival_notes}
              </p>
            ) : null}
            {booking.quiet_hours ? (
              <p style={{ marginBottom: 0, fontSize: "0.9rem", color: "var(--text-muted)" }}>
                <strong>Quiet Hours:</strong> {booking.quiet_hours}
              </p>
            ) : null}
            {booking.pet_policy ? (
              <p style={{ marginBottom: 0, fontSize: "0.9rem", color: "var(--text-muted)" }}>
                <strong>Pet Policy:</strong> {booking.pet_policy}
              </p>
            ) : null}
          </section>

          {booking.can_update_arrival ? (
            <section className="card-panel">
              <h2>Arrival Time &amp; Notes</h2>
              <p style={{ color: "var(--text-muted)", fontSize: "0.92rem" }}>
                Let our reception team know when you expect to arrive (e.g.{" "}
                <code>16:30</code> or <code>16:00-18:00</code>) or share arrival notes.
              </p>
              <form onSubmit={onSaveArrival} style={{ display: "grid", gap: "1rem" }}>
                <div className="field">
                  <label htmlFor="booking-eta">
                    Estimated Arrival Time (24-hour HH:MM or HH:MM-HH:MM)
                  </label>
                  <input
                    id="booking-eta"
                    type="text"
                    maxLength={32}
                    value={eta}
                    onChange={(e) => setEta(e.target.value)}
                    placeholder="e.g. 16:30 or 16:00-18:00"
                  />
                </div>
                <div className="field">
                  <label htmlFor="booking-arrival-notes">
                    Arrival Notes (max 500 characters, single paragraph)
                  </label>
                  <textarea
                    id="booking-arrival-notes"
                    rows={3}
                    maxLength={500}
                    value={arrivalNotes}
                    onChange={(e) => setArrivalNotes(e.target.value.replace(/[\r\n]+/g, " "))}
                    placeholder="e.g. Arriving after 18:00 by train, 1 well-behaved dog"
                  />
                </div>
                <div>
                  <button type="submit" className="btn btn-primary" disabled={savingArrival}>
                    {savingArrival ? "Saving…" : "Save Arrival Details"}
                  </button>
                </div>
              </form>
            </section>
          ) : null}

          {booking.can_request_change ? (
            <section className="card-panel">
              <h2>Request a Booking Change</h2>
              <p style={{ color: "var(--text-muted)", fontSize: "0.92rem" }}>
                Date or party changes are reviewed by our reservations team to check live
                unit availability and confirm any rate adjustment before modifying your
                booking.
              </p>
              <form onSubmit={onSubmitChangeRequest} style={{ display: "grid", gap: "1rem" }}>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))",
                    gap: "0.75rem",
                  }}
                >
                  <div className="field">
                    <label htmlFor="chg-arrival">Preferred Check-in</label>
                    <input
                      id="chg-arrival"
                      type="date"
                      value={reqArrival}
                      onChange={(e) => setReqArrival(e.target.value)}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="chg-departure">Preferred Check-out</label>
                    <input
                      id="chg-departure"
                      type="date"
                      value={reqDeparture}
                      onChange={(e) => setReqDeparture(e.target.value)}
                    />
                  </div>
                </div>
                <div className="portal-party-grid">
                  <div className="field">
                    <label htmlFor="chg-adults">Adults</label>
                    <input
                      id="chg-adults"
                      type="number"
                      min={1}
                      max={20}
                      value={reqAdults}
                      onChange={(e) => setReqAdults(Number(e.target.value))}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="chg-children">Children</label>
                    <input
                      id="chg-children"
                      type="number"
                      min={0}
                      max={20}
                      value={reqChildren}
                      onChange={(e) => setReqChildren(Number(e.target.value))}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="chg-infants">Infants</label>
                    <input
                      id="chg-infants"
                      type="number"
                      min={0}
                      max={20}
                      value={reqInfants}
                      onChange={(e) => setReqInfants(Number(e.target.value))}
                    />
                  </div>
                </div>
                <div className="field">
                  <label htmlFor="chg-notes">Details of Requested Change</label>
                  <textarea
                    id="chg-notes"
                    rows={3}
                    required
                    maxLength={1000}
                    value={changeNotes}
                    onChange={(e) => setChangeNotes(e.target.value)}
                    placeholder="Describe the dates, pitch/accommodation, or party adjustment you would like"
                  />
                </div>
                <div>
                  <button type="submit" className="btn btn-secondary" disabled={sendingChange}>
                    {sendingChange ? "Submitting…" : "Submit Change Request"}
                  </button>
                </div>
              </form>
            </section>
          ) : null}

          {booking.can_request_cancel ? (
            <section className="card-panel">
              <h2>Request Cancellation</h2>
              {booking.cancellation_policy ? (
                <p style={{ fontSize: "0.92rem", color: "var(--text-muted)" }}>
                  <strong>Cancellation Policy:</strong> {booking.cancellation_policy}
                </p>
              ) : null}
              <form onSubmit={onSubmitCancelRequest} style={{ display: "grid", gap: "1rem" }}>
                <div className="field">
                  <label htmlFor="cancel-note">Reason or Note (optional)</label>
                  <textarea
                    id="cancel-note"
                    rows={2}
                    maxLength={500}
                    value={cancelNote}
                    onChange={(e) => setCancelNote(e.target.value)}
                  />
                </div>
                <div>
                  <button
                    type="submit"
                    className="btn btn-danger-outline"
                    disabled={sendingCancel}
                  >
                    {sendingCancel ? "Submitting…" : "Submit Cancellation Request"}
                  </button>
                </div>
              </form>
            </section>
          ) : null}

          <section className="card-panel">
            <h2>Messages About This Stay</h2>
            {enquiries.length > 0 ? (
              <div style={{ display: "grid", gap: "1rem", marginBottom: "1.25rem" }}>
                {enquiries.map((enq) => (
                  <article key={enq.id} className="portal-booking-card">
                    <div
                      style={{
                        display: "flex",
                        flexWrap: "wrap",
                        justifyContent: "space-between",
                        gap: "0.5rem",
                      }}
                    >
                      <div>
                        <strong>{enq.subject}</strong>{" "}
                        <code style={{ fontSize: "0.85rem" }}>({enq.reference})</code>
                      </div>
                      <span className={statusBadgeClass(enq.status)}>{enq.status}</span>
                    </div>
                    {enq.messages && enq.messages.length > 0 ? (
                      <div style={{ display: "grid", gap: "0.5rem" }}>
                        {enq.messages.map((m) => (
                          <div
                            key={m.id}
                            className={`portal-thread-msg ${
                              m.sender_role === "PROPERTY" ? "portal-thread-msg-property" : ""
                            }`}
                          >
                            <div
                              style={{
                                fontSize: "0.82rem",
                                color: "var(--text-muted)",
                                marginBottom: "0.2rem",
                              }}
                            >
                              <strong>{m.sender_name}</strong> ·{" "}
                              {new Date(m.created_at).toLocaleString()}
                            </div>
                            <div style={{ whiteSpace: "pre-wrap", fontSize: "0.93rem" }}>
                              {m.body}
                            </div>
                          </div>
                        ))}
                      </div>
                    ) : null}
                    {enq.status !== "CLOSED" ? (
                      <form
                        onSubmit={(e) => onReplyBookingEnquiry(e, enq.id)}
                        style={{ display: "grid", gap: "0.5rem" }}
                      >
                        <div className="field">
                          <label htmlFor={`booking-reply-${enq.id}`}>Add a Reply</label>
                          <textarea
                            id={`booking-reply-${enq.id}`}
                            rows={2}
                            maxLength={2000}
                            required
                            value={replyDrafts[enq.id] || ""}
                            onChange={(e) =>
                              setReplyDrafts((prev) => ({
                                ...prev,
                                [enq.id]: e.target.value,
                              }))
                            }
                          />
                        </div>
                        <div>
                          <button
                            type="submit"
                            className="btn btn-secondary"
                            disabled={replyBusyId === enq.id}
                          >
                            {replyBusyId === enq.id ? "Sending…" : "Send Reply"}
                          </button>
                        </div>
                      </form>
                    ) : null}
                  </article>
                ))}
              </div>
            ) : null}

            <form onSubmit={onAskBookingQuestion} style={{ display: "grid", gap: "0.9rem" }}>
              <div className="field">
                <label htmlFor="booking-question-subject">Question Subject</label>
                <input
                  id="booking-question-subject"
                  type="text"
                  required
                  maxLength={160}
                  value={questionSubject}
                  onChange={(e) => setQuestionSubject(e.target.value)}
                  placeholder="e.g. Electric hook-up or firewood question"
                />
              </div>
              <div className="field">
                <label htmlFor="booking-question-body">Message</label>
                <textarea
                  id="booking-question-body"
                  rows={3}
                  required
                  maxLength={2000}
                  value={questionBody}
                  onChange={(e) => setQuestionBody(e.target.value)}
                />
              </div>
              <div>
                <button type="submit" className="btn btn-secondary" disabled={sendingQuestion}>
                  {sendingQuestion ? "Sending…" : "Message Reservations Team"}
                </button>
              </div>
            </form>
          </section>
        </div>

        <aside className="card-panel" style={{ minWidth: 0 }}>
          <h2>Receipt &amp; Payment Summary</h2>
          {booking.is_ota_managed ? (
            <div className="alert alert-warn" style={{ margin: 0 }}>
              {booking.billing_notice}
            </div>
          ) : (
            <>
              {booking.quote ? (
                <div style={{ marginBottom: "1rem" }}>
                  <div className="summary-row">
                    <span>Accommodation ({booking.quote.nights} nights)</span>
                    <span>{formatMoney(booking.quote.subtotal_pence, currency)}</span>
                  </div>
                  {booking.quote.line_items?.map((item, idx) => (
                    <div key={`${item.description}-${idx}`} className="summary-row">
                      <span>
                        {item.description} × {item.quantity}
                      </span>
                      <span>{formatMoney(item.total_pence, currency)}</span>
                    </div>
                  ))}
                  {booking.quote.tax_pence > 0 ? (
                    <div className="summary-row" style={{ color: "var(--text-muted)" }}>
                      <span>Included VAT / Tax</span>
                      <span>{formatMoney(booking.quote.tax_pence, currency)}</span>
                    </div>
                  ) : null}
                </div>
              ) : null}

              <div className="summary-row summary-total">
                <span>Total Stay</span>
                <span>{formatMoney(booking.total_pence || 0, currency)}</span>
              </div>
              <div className="summary-row">
                <span>Amount Paid</span>
                <strong>{formatMoney(booking.amount_paid_pence || 0, currency)}</strong>
              </div>
              <div className="summary-row">
                <span>Balance Due</span>
                <strong>{formatMoney(booking.balance_due_pence || 0, currency)}</strong>
              </div>

              {booking.payments && booking.payments.length > 0 ? (
                <div
                  style={{
                    marginTop: "1rem",
                    borderTop: "1px solid var(--border-subtle)",
                    paddingTop: "0.75rem",
                  }}
                >
                  <strong style={{ fontSize: "0.9rem" }}>Recorded Payments</strong>
                  {booking.payments.map((p, idx) => (
                    <div
                      key={`${p.paid_at || "payment"}-${idx}`}
                      className="summary-row"
                      style={{ fontSize: "0.88rem" }}
                    >
                      <span>{p.status}</span>
                      <span>{formatMoney(p.amount_pence, p.currency)}</span>
                    </div>
                  ))}
                </div>
              ) : null}
            </>
          )}

          {booking.contact_email || booking.contact_phone ? (
            <div
              style={{
                marginTop: "1.25rem",
                fontSize: "0.88rem",
                color: "var(--text-muted)",
                overflowWrap: "anywhere",
              }}
            >
              <strong>Need Help?</strong>
              {booking.contact_email ? <div>Email: {booking.contact_email}</div> : null}
              {booking.contact_phone ? <div>Phone: {booking.contact_phone}</div> : null}
            </div>
          ) : null}
        </aside>
      </div>
    </div>
  );
}

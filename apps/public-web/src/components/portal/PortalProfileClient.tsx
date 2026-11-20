"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { portalFetch, PortalClientError, statusBadgeClass } from "@/lib/portal-client";
import type {
  PortalEnquiryView,
  PortalGuestPreferenceView,
  PortalProfileView,
  PortalSessionView,
} from "@/lib/types";
import { PortalNavBar, StepUpVerifyBanner } from "./PortalNavBar";

export function PortalProfileClient() {
  const router = useRouter();
  const [session, setSession] = useState<PortalSessionView | null>(null);
  const [profile, setProfile] = useState<PortalProfileView | null>(null);
  const [enquiries, setEnquiries] = useState<PortalEnquiryView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [info, setInfo] = useState("");

  // Contact & structured stay preferences form
  const [phone, setPhone] = useState("");
  const [contactMethod, setContactMethod] = useState("EMAIL");
  const [preferredLang, setPreferredLang] = useState("en");
  const [preferences, setPreferences] = useState<PortalGuestPreferenceView[]>([]);
  const [newPrefCategory, setNewPrefCategory] = useState("ARRIVAL");
  const [newPrefKey, setNewPrefKey] = useState("");
  const [newPrefValue, setNewPrefValue] = useState("");
  const [savingProfile, setSavingProfile] = useState(false);

  // Email change form
  const [newEmail, setNewEmail] = useState("");
  const [emailChangeCode, setEmailChangeCode] = useState("");
  const [changingEmail, setChangingEmail] = useState(false);

  // New enquiry form
  const [enqCategory, setEnqCategory] = useState("GENERAL");
  const [enqBookingRef, setEnqBookingRef] = useState("");
  const [enqSubject, setEnqSubject] = useState("");
  const [enqMessage, setEnqMessage] = useState("");
  const [sendingEnq, setSendingEnq] = useState(false);

  // Reply state per enquiry
  const [replyDrafts, setReplyDrafts] = useState<Record<string, string>>({});
  const [replyBusyId, setReplyBusyId] = useState("");

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
        const [profRes, enqRes] = await Promise.all([
          portalFetch<{ profile: PortalProfileView }>("/api/public/portal/profile"),
          portalFetch<{ enquiries: PortalEnquiryView[] }>("/api/public/portal/enquiries"),
        ]);
        if (!active) return;
        setSession(sessRes.session);
        setProfile(profRes.profile);
        setEnquiries(enqRes.enquiries || []);
        setPhone(profRes.profile.phone || "");
        setContactMethod(profRes.profile.preferred_contact_method || "EMAIL");
        setPreferredLang(profRes.profile.preferred_language || "en");
        setPreferences(profRes.profile.preferences || []);
      } catch (err) {
        if (!active) return;
        if (err instanceof PortalClientError && err.status === 401) {
          router.replace("/portal");
          return;
        }
        setError(
          err instanceof PortalClientError ? err.message : "Unable to load your profile.",
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

  function onAddPreference() {
    const cleanKey = newPrefKey
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, "_")
      .replace(/^_+|_+$/g, "");
    const cleanVal = newPrefValue.trim();
    if (!cleanKey || !cleanVal) return;
    setPreferences((prev) => {
      const filtered = prev.filter(
        (p) => !(p.category === newPrefCategory && p.preference_key === cleanKey),
      );
      return [
        ...filtered,
        {
          category: newPrefCategory,
          preference_key: cleanKey,
          preference_value: cleanVal,
        },
      ];
    });
    setNewPrefKey("");
    setNewPrefValue("");
  }

  function onRemovePreference(category: string, key: string) {
    setPreferences((prev) =>
      prev.filter((p) => !(p.category === category && p.preference_key === key)),
    );
  }

  async function onSaveProfile(e: FormEvent) {
    e.preventDefault();
    if (!session) return;
    setSavingProfile(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<{ profile: PortalProfileView }>(
        "/api/public/portal/profile",
        {
          method: "PATCH",
          body: {
            phone,
            preferred_contact_method: contactMethod,
            preferred_language: preferredLang,
            preferences,
          },
          csrfToken: session.csrf_token,
        },
      );
      setProfile(res.profile);
      setPreferences(res.profile.preferences || []);
      setInfo("Your contact and stay preferences have been updated.");
    } catch (err) {
      setError(err instanceof PortalClientError ? err.message : "Failed to save profile.");
    } finally {
      setSavingProfile(false);
    }
  }

  async function onInitiateEmailChange(e: FormEvent) {
    e.preventDefault();
    if (!session) return;
    setChangingEmail(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<{ accepted: boolean; message: string }>(
        "/api/public/portal/profile/email-change",
        {
          method: "POST",
          body: { new_email: newEmail.trim() },
          csrfToken: session.csrf_token,
        },
      );
      const profRes = await portalFetch<{ profile: PortalProfileView }>(
        "/api/public/portal/profile",
      );
      setProfile(profRes.profile);
      setInfo(res.message);
    } catch (err) {
      if (err instanceof PortalClientError && err.code === "step_up_required") {
        setSession((prev) => (prev ? { ...prev, step_up_fresh: false } : prev));
      }
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Unable to initiate email verification.",
      );
    } finally {
      setChangingEmail(false);
    }
  }

  async function onConfirmEmailChange(e: FormEvent) {
    e.preventDefault();
    if (!session) return;
    setChangingEmail(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<{ profile: PortalProfileView }>(
        "/api/public/portal/profile/email-change/confirm",
        {
          method: "POST",
          body: { code: emailChangeCode.trim() },
          csrfToken: session.csrf_token,
        },
      );
      const sessRes = await portalFetch<{ session: PortalSessionView }>(
        "/api/public/portal/auth/session",
      );
      setSession(sessRes.session);
      setProfile(res.profile);
      setNewEmail("");
      setEmailChangeCode("");
      setInfo(
        "Your verified email address has been updated and other active sessions were signed out.",
      );
    } catch (err) {
      setError(
        err instanceof PortalClientError
          ? err.message
          : "Invalid or expired email verification code.",
      );
    } finally {
      setChangingEmail(false);
    }
  }

  async function onCreateEnquiry(e: FormEvent) {
    e.preventDefault();
    if (!session) return;
    setSendingEnq(true);
    setError("");
    setInfo("");
    try {
      const res = await portalFetch<{ enquiry: PortalEnquiryView }>(
        "/api/public/portal/enquiries",
        {
          method: "POST",
          body: {
            category: enqCategory,
            booking_reference: enqBookingRef.trim(),
            subject: enqSubject.trim(),
            message: enqMessage.trim(),
          },
          csrfToken: session.csrf_token,
        },
      );
      setEnquiries((prev) => [res.enquiry, ...prev]);
      setEnqBookingRef("");
      setEnqSubject("");
      setEnqMessage("");
      setInfo(`Enquiry ${res.enquiry.reference} submitted.`);
    } catch (err) {
      setError(err instanceof PortalClientError ? err.message : "Unable to submit enquiry.");
    } finally {
      setSendingEnq(false);
    }
  }

  async function onReplyEnquiry(e: FormEvent, enquiryId: string) {
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
        <p style={{ margin: 0, color: "var(--text-muted)" }}>
          Loading your profile &amp; enquiries…
        </p>
      </div>
    );
  }

  if (!session || !profile) {
    return (
      <div className="card-panel">
        <div className="alert alert-err" role="alert">
          {error || "Profile unavailable."}
        </div>
      </div>
    );
  }

  return (
    <div>
      <PortalNavBar session={session} activeSection="profile" />
      <h1 className="section-title">Profile &amp; Support Enquiries</h1>

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

      <div className="cards-grid-2" style={{ marginBottom: "2rem" }}>
        <section className="card-panel" style={{ minWidth: 0 }}>
          <h2>Contact Details, Language &amp; Stay Preferences</h2>
          <p
            style={{
              fontSize: "0.92rem",
              color: "var(--text-muted)",
              overflowWrap: "anywhere",
            }}
          >
            Name: <strong>{profile.full_name}</strong> · Verified Email:{" "}
            <strong>{profile.verified_email}</strong>
          </p>
          {profile.is_do_not_contact ? (
            <div className="alert alert-warn">
              Your profile has Do-Not-Contact status active. Transactional stay access and
              privacy rights remain available; please send a support enquiry below if you wish
              to update your contact details.
            </div>
          ) : (
            <form onSubmit={onSaveProfile} style={{ display: "grid", gap: "1rem" }}>
              <div className="field">
                <label htmlFor="prof-phone">Telephone Number</label>
                <input
                  id="prof-phone"
                  type="tel"
                  autoComplete="tel"
                  value={phone}
                  onChange={(e) => setPhone(e.target.value)}
                />
              </div>
              <div className="field">
                <label htmlFor="prof-method">Preferred Contact Method</label>
                <select
                  id="prof-method"
                  value={contactMethod}
                  onChange={(e) => setContactMethod(e.target.value)}
                >
                  <option value="EMAIL">Email</option>
                  <option value="PHONE">Phone</option>
                  <option value="SMS">SMS</option>
                  <option value="POST">Post</option>
                </select>
              </div>
              <div className="field">
                <label htmlFor="prof-lang">Preferred Communication Language</label>
                <select
                  id="prof-lang"
                  value={preferredLang}
                  onChange={(e) => setPreferredLang(e.target.value)}
                >
                  <option value="en">English (en)</option>
                  <option value="fr">Français (fr)</option>
                  <option value="de">Deutsch (de)</option>
                  <option value="es">Español (es)</option>
                  <option value="nl">Nederlands (nl)</option>
                  <option value="it">Italiano (it)</option>
                </select>
              </div>

              <div
                style={{
                  borderTop: "1px solid var(--border-subtle)",
                  paddingTop: "0.85rem",
                  display: "grid",
                  gap: "0.75rem",
                }}
              >
                <strong style={{ fontSize: "0.92rem" }}>Stay Preferences</strong>
                {preferences.length > 0 ? (
                  <ul
                    style={{
                      listStyle: "none",
                      padding: 0,
                      margin: 0,
                      display: "grid",
                      gap: "0.45rem",
                      fontSize: "0.9rem",
                    }}
                  >
                    {preferences.map((pref) => (
                      <li
                        key={`${pref.category}:${pref.preference_key}`}
                        style={{
                          display: "flex",
                          flexWrap: "wrap",
                          justifyContent: "space-between",
                          gap: "0.5rem",
                          alignItems: "center",
                          padding: "0.4rem 0.6rem",
                          borderRadius: "var(--radius-sm)",
                          background: "var(--bg-tint)",
                        }}
                      >
                        <span>
                          <strong>{pref.category}</strong> · {pref.preference_key}:{" "}
                          {pref.preference_value}
                        </span>
                        <button
                          type="button"
                          className="btn btn-secondary"
                          style={{ padding: "0.2rem 0.55rem", fontSize: "0.82rem" }}
                          onClick={() =>
                            onRemovePreference(pref.category, pref.preference_key)
                          }
                        >
                          Remove
                        </button>
                      </li>
                    ))}
                  </ul>
                ) : null}

                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(auto-fit, minmax(120px, 1fr))",
                    gap: "0.5rem",
                  }}
                >
                  <div className="field">
                    <label htmlFor="pref-cat">Category</label>
                    <select
                      id="pref-cat"
                      value={newPrefCategory}
                      onChange={(e) => setNewPrefCategory(e.target.value)}
                    >
                      <option value="ARRIVAL">Arrival</option>
                      <option value="ACCESSIBILITY">Accessibility</option>
                      <option value="COMMUNICATION">Communication</option>
                      <option value="PET">Pets</option>
                    </select>
                  </div>
                  <div className="field">
                    <label htmlFor="pref-key">Preference Key</label>
                    <input
                      id="pref-key"
                      type="text"
                      maxLength={64}
                      value={newPrefKey}
                      onChange={(e) => setNewPrefKey(e.target.value)}
                      placeholder="e.g. dogs"
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="pref-val">Value</label>
                    <input
                      id="pref-val"
                      type="text"
                      maxLength={200}
                      value={newPrefValue}
                      onChange={(e) => setNewPrefValue(e.target.value)}
                      placeholder="e.g. 1 border collie"
                    />
                  </div>
                </div>
                <div>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    style={{ padding: "0.4rem 0.85rem", fontSize: "0.88rem" }}
                    onClick={onAddPreference}
                  >
                    Add Stay Preference
                  </button>
                </div>
              </div>

              <div>
                <button type="submit" className="btn btn-primary" disabled={savingProfile}>
                  {savingProfile ? "Saving…" : "Save Contact & Stay Preferences"}
                </button>
              </div>
            </form>
          )}
        </section>

        <section className="card-panel" style={{ minWidth: 0 }}>
          <h2>Change Primary Email Address</h2>
          <p style={{ fontSize: "0.92rem", color: "var(--text-muted)" }}>
            Changing your login email sends a 6-digit verification code to the new address,
            alerts your previous email address, and signs out any other active sessions.
          </p>

          {!profile.is_do_not_contact ? (
            <>
              <form
                onSubmit={onInitiateEmailChange}
                style={{ display: "grid", gap: "1rem", marginBottom: "1rem" }}
              >
                <div className="field">
                  <label htmlFor="new-portal-email">New Email Address</label>
                  <input
                    id="new-portal-email"
                    type="email"
                    required
                    value={newEmail}
                    onChange={(e) => setNewEmail(e.target.value)}
                    placeholder="new.address@example.com"
                  />
                </div>
                <div>
                  <button
                    type="submit"
                    className="btn btn-secondary"
                    disabled={changingEmail || !session.step_up_fresh}
                  >
                    {changingEmail ? "Sending…" : "Send Verification Code to New Email"}
                  </button>
                </div>
              </form>

              {profile.pending_email ? (
                <form
                  onSubmit={onConfirmEmailChange}
                  style={{
                    display: "grid",
                    gap: "0.85rem",
                    paddingTop: "1rem",
                    borderTop: "1px solid var(--border-subtle)",
                  }}
                >
                  <div style={{ fontSize: "0.9rem", overflowWrap: "anywhere" }}>
                    Pending verification for <strong>{profile.pending_email}</strong>:
                  </div>
                  <div className="field">
                    <label htmlFor="confirm-email-code">
                      6-Digit Code Sent to New Email
                    </label>
                    <input
                      id="confirm-email-code"
                      type="text"
                      inputMode="numeric"
                      maxLength={6}
                      required
                      value={emailChangeCode}
                      onChange={(e) => setEmailChangeCode(e.target.value)}
                      placeholder="123456"
                    />
                  </div>
                  <div>
                    <button type="submit" className="btn btn-primary" disabled={changingEmail}>
                      Confirm New Email Address
                    </button>
                  </div>
                </form>
              ) : null}
            </>
          ) : null}
        </section>
      </div>

      <section className="card-panel" style={{ marginBottom: "2rem" }}>
        <h2>Message Our Reservations Team</h2>
        <form onSubmit={onCreateEnquiry} style={{ display: "grid", gap: "1rem" }}>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
              gap: "0.75rem",
            }}
          >
            <div className="field">
              <label htmlFor="enq-cat">Topic</label>
              <select
                id="enq-cat"
                value={enqCategory}
                onChange={(e) => setEnqCategory(e.target.value)}
              >
                <option value="GENERAL">General Question</option>
                <option value="BOOKING">Booking Question</option>
                <option value="AVAILABILITY">Availability Question</option>
                <option value="ACCESSIBILITY">Accessibility</option>
                <option value="SPECIAL_REQUEST">Special Request</option>
                <option value="COMPLAINT">Feedback / Complaint</option>
                <option value="OTHER">Other</option>
              </select>
            </div>
            <div className="field">
              <label htmlFor="enq-ref">Related Booking Reference (optional)</label>
              <input
                id="enq-ref"
                type="text"
                value={enqBookingRef}
                onChange={(e) => setEnqBookingRef(e.target.value)}
                placeholder="e.g. LV-2026-AB12CD"
              />
            </div>
          </div>
          <div className="field">
            <label htmlFor="enq-subject">Subject</label>
            <input
              id="enq-subject"
              type="text"
              required
              maxLength={160}
              value={enqSubject}
              onChange={(e) => setEnqSubject(e.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="enq-body">Message</label>
            <textarea
              id="enq-body"
              rows={3}
              required
              maxLength={2000}
              value={enqMessage}
              onChange={(e) => setEnqMessage(e.target.value)}
            />
          </div>
          <div>
            <button type="submit" className="btn btn-primary" disabled={sendingEnq}>
              {sendingEnq ? "Sending…" : "Send Enquiry"}
            </button>
          </div>
        </form>
      </section>

      <section>
        <h2 className="section-title" style={{ fontSize: "1.4rem" }}>
          Your Support Threads
        </h2>
        {enquiries.length === 0 ? (
          <div className="card-panel">
            <p style={{ margin: 0, color: "var(--text-muted)" }}>
              You have no support threads yet.
            </p>
          </div>
        ) : (
          <div style={{ display: "grid", gap: "1rem" }}>
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
                    {enq.reservation_reference ? (
                      <span style={{ fontSize: "0.85rem", color: "var(--text-muted)" }}>
                        {" "}
                        · Booking {enq.reservation_reference}
                      </span>
                    ) : null}
                  </div>
                  <span className={statusBadgeClass(enq.status)}>{enq.status}</span>
                </div>

                {enq.messages && enq.messages.length > 0 ? (
                  <div style={{ display: "grid", gap: "0.6rem" }}>
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
                            marginBottom: "0.25rem",
                          }}
                        >
                          <strong>{m.sender_name}</strong> ·{" "}
                          {new Date(m.created_at).toLocaleString()}
                        </div>
                        <div style={{ whiteSpace: "pre-wrap", fontSize: "0.94rem" }}>
                          {m.body}
                        </div>
                      </div>
                    ))}
                  </div>
                ) : null}

                {enq.status !== "CLOSED" ? (
                  <form
                    onSubmit={(e) => onReplyEnquiry(e, enq.id)}
                    style={{ display: "grid", gap: "0.6rem" }}
                  >
                    <div className="field">
                      <label htmlFor={`reply-${enq.id}`}>Add a Reply</label>
                      <textarea
                        id={`reply-${enq.id}`}
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
        )}
      </section>
    </div>
  );
}

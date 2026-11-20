"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import { BookingWizard } from "@/components/BookingWizard";
import { buildPropertyBrandCssVars, getPublicLogoSrc } from "@/lib/branding";
import {
  formatAmenity,
  formatCategoryLabel,
  formatMoney,
  formatStayDate,
  getUnitTypeGallery,
} from "@/lib/format";
import type {
  AvailabilitySearchItem,
  BookingQuote,
  PublicAvailabilityResponse,
  PublicExtraView,
  PublicPropertyView,
  PublicUnitTypeView,
} from "@/lib/types";

interface SelectedStay {
  unitType: PublicUnitTypeView;
  quote: BookingQuote;
}

export function EmbedBookingWidget({
  initialProperty,
  extras,
  initialAvailability,
  initialArrival,
  initialDeparture,
  initialAdults,
  initialChildren,
  initialInfants,
  initialCategory = "",
  initialSelectedStay = null,
  initialError = "",
}: {
  initialProperty: PublicPropertyView;
  extras: PublicExtraView[];
  initialAvailability: PublicAvailabilityResponse | null;
  initialArrival: string;
  initialDeparture: string;
  initialAdults: number;
  initialChildren: number;
  initialInfants: number;
  initialCategory?: string;
  initialSelectedStay?: SelectedStay | null;
  initialError?: string;
}) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [activeProperty, setActiveProperty] =
    useState<PublicPropertyView>(initialProperty);
  const [arrival, setArrival] = useState(initialArrival);
  const [departure, setDeparture] = useState(initialDeparture);
  const [adults, setAdults] = useState(initialAdults);
  const [childrenCount, setChildrenCount] = useState(initialChildren);
  const [infants, setInfants] = useState(initialInfants);

  const [availability, setAvailability] =
    useState<PublicAvailabilityResponse | null>(initialAvailability);
  const [searching, setSearching] = useState(false);
  const [quotingCode, setQuotingCode] = useState("");
  const [errorMsg, setErrorMsg] = useState(initialError);
  const [selectedStay, setSelectedStay] = useState<SelectedStay | null>(
    initialSelectedStay,
  );

  const cssVars = buildPropertyBrandCssVars(activeProperty.branding);
  const displayName =
    activeProperty.branding?.display_name || activeProperty.name;
  const tagline =
    activeProperty.branding?.tagline ||
    activeProperty.tagline ||
    activeProperty.region;
  const logoSrc = getPublicLogoSrc(
    activeProperty.branding,
    activeProperty.slug,
  );

  // Notify parent website iframe resizer whenever widget height changes
  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof window === "undefined") return;

    const postHeight = () => {
      if (!window.parent || window.parent === window) return;
      const h = Math.ceil(el.getBoundingClientRect().height);
      if (h > 0) {
        window.parent.postMessage(
          {
            type: "lovelace:embed:resize",
            slug: activeProperty.slug,
            height: h,
          },
          "*",
        );
      }
    };

    postHeight();
    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(() => postHeight());
      ro.observe(el);
    }
    window.addEventListener("resize", postHeight);
    return () => {
      if (ro) ro.disconnect();
      window.removeEventListener("resize", postHeight);
    };
  }, [activeProperty.slug, availability, selectedStay, errorMsg]);

  async function handleSearch(e: FormEvent) {
    e.preventDefault();
    if (!arrival || !departure || departure <= arrival) {
      setErrorMsg("Departure date must be after arrival date.");
      return;
    }

    setSearching(true);
    setErrorMsg("");
    setSelectedStay(null);

    try {
      const res = await fetch("/api/public/availability", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          property_slug: activeProperty.slug,
          arrival_date: arrival,
          departure_date: departure,
          adults,
          children: childrenCount,
          infants,
          category: initialCategory || undefined,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setErrorMsg(
          data.error?.message || "Could not check availability for those dates.",
        );
        return;
      }
      const typed = data as PublicAvailabilityResponse;
      setAvailability(typed);
      if (typed.property) {
        setActiveProperty(typed.property);
      }
    } catch {
      setErrorMsg("Unable to connect to the booking service right now.");
    } finally {
      setSearching(false);
    }
  }

  async function handleSelectStay(item: AvailabilitySearchItem) {
    const searchArr = availability?.arrival_date || arrival;
    const searchDep = availability?.departure_date || departure;
    setQuotingCode(item.unit_type.code);
    setErrorMsg("");

    try {
      const res = await fetch("/api/public/pricing", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          property_slug: activeProperty.slug,
          unit_type_code: item.unit_type.code,
          arrival_date: searchArr,
          departure_date: searchDep,
          adults,
          children: childrenCount,
          infants,
          extras: [],
        }),
      });
      const data = await res.json();
      if (!res.ok || !data.quote) {
        setErrorMsg(
          data.error?.message ||
            "This accommodation could not be quoted right now.",
        );
        return;
      }
      setArrival(searchArr);
      setDeparture(searchDep);
      setSelectedStay({
        unitType: item.unit_type,
        quote: data.quote as BookingQuote,
      });
    } catch {
      setErrorMsg("Unable to prepare your booking quote right now.");
    } finally {
      setQuotingCode("");
    }
  }

  const results = availability?.items || [];
  const fullPageUrl = `/stays?property=${encodeURIComponent(activeProperty.slug)}&arrival=${encodeURIComponent(arrival)}&departure=${encodeURIComponent(departure)}&adults=${adults}&children=${childrenCount}&infants=${infants}`;

  return (
    <div
      ref={rootRef}
      style={{
        ...cssVars,
        background: "var(--bg-page)",
        padding: "1rem",
        borderRadius: "12px",
      }}
    >
      <div
        style={{
          maxWidth: "1080px",
          marginInline: "auto",
          display: "grid",
          gap: "1rem",
        }}
      >
        <header
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            flexWrap: "wrap",
            gap: "0.75rem",
            padding: "0.9rem 1.1rem",
            background: "#ffffff",
            border: "1px solid var(--border-subtle)",
            borderRadius: "12px",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
            {logoSrc ? (
              <img
                src={logoSrc}
                alt={activeProperty.branding?.logo_alt || `${displayName} logo`}
                className="brand-mark-logo"
              />
            ) : null}
            <div>
              <h1
                style={{
                  margin: 0,
                  fontSize: "1.25rem",
                  color: "var(--property-brand, var(--brand-forest))",
                }}
              >
                {displayName}
              </h1>
              {tagline ? (
                <div style={{ fontSize: "0.84rem", color: "var(--text-muted)" }}>
                  {tagline}
                </div>
              ) : null}
            </div>
          </div>

          <a
            href={fullPageUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="btn btn-secondary"
            style={{ padding: "0.45rem 0.85rem", fontSize: "0.84rem" }}
          >
            Open Full Booking Page ↗
          </a>
        </header>

        {!selectedStay ? (
          <>
            <form
              onSubmit={handleSearch}
              className="search-bar-card"
              aria-label="Search accommodation availability"
            >
              <div className="search-grid">
                <div className="field">
                  <label htmlFor="embed-arrival">Check-in</label>
                  <input
                    id="embed-arrival"
                    name="arrival"
                    type="date"
                    value={arrival}
                    onChange={(e) => setArrival(e.target.value)}
                    required
                  />
                </div>

                <div className="field">
                  <label htmlFor="embed-departure">Check-out</label>
                  <input
                    id="embed-departure"
                    name="departure"
                    type="date"
                    value={departure}
                    onChange={(e) => setDeparture(e.target.value)}
                    required
                  />
                </div>

                <div className="field">
                  <label htmlFor="embed-adults">Adults</label>
                  <select
                    id="embed-adults"
                    name="adults"
                    value={adults}
                    onChange={(e) => setAdults(Number(e.target.value))}
                  >
                    {[1, 2, 3, 4, 5, 6, 7, 8].map((n) => (
                      <option key={n} value={n}>
                        {n} {n === 1 ? "Adult" : "Adults"}
                      </option>
                    ))}
                  </select>
                </div>

                <div className="field">
                  <label htmlFor="embed-children">Children (2–15)</label>
                  <select
                    id="embed-children"
                    name="children"
                    value={childrenCount}
                    onChange={(e) => setChildrenCount(Number(e.target.value))}
                  >
                    {[0, 1, 2, 3, 4, 5, 6].map((n) => (
                      <option key={n} value={n}>
                        {n} {n === 1 ? "Child" : "Children"}
                      </option>
                    ))}
                  </select>
                </div>

                <div className="field">
                  <label htmlFor="embed-infants">Infants</label>
                  <select
                    id="embed-infants"
                    name="infants"
                    value={infants}
                    onChange={(e) => setInfants(Number(e.target.value))}
                  >
                    {[0, 1, 2].map((n) => (
                      <option key={n} value={n}>
                        {n} {n === 1 ? "Infant" : "Infants"}
                      </option>
                    ))}
                  </select>
                </div>

                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={searching}
                >
                  {searching ? "Checking..." : "Check Availability"}
                </button>
              </div>
            </form>

            {errorMsg && (
              <div className="alert alert-err" role="alert">
                {errorMsg}
              </div>
            )}

            {!errorMsg && availability && results.length === 0 && (
              <div className="card-panel" role="status">
                <h2
                  style={{
                    fontSize: "1.15rem",
                    color: "var(--property-brand, var(--brand-forest))",
                    marginBottom: "0.4rem",
                  }}
                >
                  No accommodation available for {formatStayDate(availability.arrival_date)} –{" "}
                  {formatStayDate(availability.departure_date)}
                </h2>
                <p style={{ margin: 0, color: "var(--text-muted)", fontSize: "0.92rem" }}>
                  Try adjusting your check-in/check-out dates or guest count above
                  (minimum stay rules may apply on weekends and peak dates).
                </p>
              </div>
            )}

            {results.length > 0 && (
              <div className="cards-grid-2">
                {results.map((item) => {
                  const ut = item.unit_type;
                  const gallery = getUnitTypeGallery(ut);
                  const isQuoting = quotingCode === ut.code;
                  return (
                    <article key={ut.id} className="stay-card">
                      <div className="stay-card-img-wrap">
                        <img
                          src={gallery[0].url}
                          alt={gallery[0].alt}
                          className="stay-card-img"
                        />
                      </div>
                      <div className="stay-card-body">
                        <div
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            alignItems: "center",
                            gap: "0.5rem",
                          }}
                        >
                          <span className="badge badge-ok">
                            {item.available_units} available
                          </span>
                          <span className="badge">
                            {formatCategoryLabel(ut.category)}
                          </span>
                        </div>

                        <h2
                          style={{
                            margin: 0,
                            fontSize: "1.3rem",
                            color: "var(--property-brand, var(--brand-forest))",
                          }}
                        >
                          {ut.name}
                        </h2>

                        {ut.description ? (
                          <p
                            style={{
                              margin: 0,
                              color: "var(--text-muted)",
                              fontSize: "0.92rem",
                            }}
                          >
                            {ut.description}
                          </p>
                        ) : null}

                        <div className="chip-list">
                          <span className="chip">Max {ut.max_guests} guests</span>
                          <span className="chip">
                            {ut.pets_allowed ? "Pets welcome" : "No pets"}
                          </span>
                          {ut.amenities.slice(0, 3).map((am) => (
                            <span key={am} className="chip">
                              {formatAmenity(am)}
                            </span>
                          ))}
                        </div>

                        <div
                          style={{
                            marginTop: "auto",
                            paddingTop: "0.85rem",
                            borderTop: "1px solid var(--border-subtle)",
                            display: "flex",
                            justifyContent: "space-between",
                            alignItems: "center",
                            gap: "0.75rem",
                            flexWrap: "wrap",
                          }}
                        >
                          <div>
                            <div
                              style={{
                                fontSize: "1.2rem",
                                fontWeight: 700,
                                color: "var(--property-brand, var(--brand-forest))",
                              }}
                            >
                              {formatMoney(item.total_pence, item.currency)}
                            </div>
                            <div
                              style={{
                                fontSize: "0.8rem",
                                color: "var(--text-muted)",
                              }}
                            >
                              {item.nights} {item.nights === 1 ? "night" : "nights"} ·{" "}
                              {item.balance_due_pence > 0
                                ? `${formatMoney(item.deposit_due_pence, item.currency)} deposit today`
                                : "Full balance at checkout"}
                            </div>
                          </div>

                          <button
                            type="button"
                            className="btn btn-primary"
                            disabled={Boolean(quotingCode)}
                            onClick={() => handleSelectStay(item)}
                          >
                            {isQuoting ? "Loading..." : "Select & Book"}
                          </button>
                        </div>
                      </div>
                    </article>
                  );
                })}
              </div>
            )}
          </>
        ) : (
          <BookingWizard
            property={activeProperty}
            unitType={selectedStay.unitType}
            extras={extras}
            initialQuote={selectedStay.quote}
            arrival={arrival}
            departure={departure}
            adults={adults}
            childrenCount={childrenCount}
            infants={infants}
            embedMode
            onBackToSearch={() => setSelectedStay(null)}
          />
        )}

        <footer
          style={{
            display: "flex",
            justifyContent: "space-between",
            flexWrap: "wrap",
            gap: "0.5rem",
            fontSize: "0.78rem",
            color: "var(--text-muted)",
            paddingInline: "0.25rem",
          }}
        >
          <span>
            Check-in {activeProperty.check_in_time} · Check-out{" "}
            {activeProperty.check_out_time}
          </span>
          <span>Direct booking powered by LOVELACE</span>
        </footer>
      </div>
    </div>
  );
}

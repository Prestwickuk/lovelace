"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";

export interface SearchBarValues {
  arrival: string;
  departure: string;
  adults: number;
  children: number;
  infants: number;
  category?: string;
}

export function SearchBar({
  initial,
  targetPath = "/stays",
  compact = false,
  propertySlug,
}: {
  initial: SearchBarValues;
  targetPath?: string;
  compact?: boolean;
  propertySlug?: string;
}) {
  const router = useRouter();
  const [arrival, setArrival] = useState(initial.arrival);
  const [departure, setDeparture] = useState(initial.departure);
  const [adults, setAdults] = useState(initial.adults);
  const [children, setChildren] = useState(initial.children);
  const [infants, setInfants] = useState(initial.infants);
  const [error, setError] = useState("");

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!arrival || !departure || departure <= arrival) {
      setError("Departure date must be after arrival date.");
      return;
    }
    setError("");
    const q = new URLSearchParams({
      arrival,
      departure,
      adults: String(adults),
      children: String(children),
      infants: String(infants),
    });
    if (propertySlug) {
      q.set("property", propertySlug);
    }
    if (initial.category) {
      q.set("category", initial.category);
    }
    router.push(`${targetPath}?${q.toString()}`);
  }

  return (
    <form
      onSubmit={onSubmit}
      className="search-bar-card"
      aria-label="Search accommodation availability"
    >
      <div
        className="search-grid"
        style={compact ? { gridTemplateColumns: "repeat(2, minmax(0, 1fr))" } : undefined}
      >
        <div className="field">
          <label htmlFor="search-arrival">Arrival date</label>
          <input
            id="search-arrival"
            name="arrival"
            type="date"
            value={arrival}
            onChange={(e) => setArrival(e.target.value)}
            required
          />
        </div>

        <div className="field">
          <label htmlFor="search-departure">Departure date</label>
          <input
            id="search-departure"
            name="departure"
            type="date"
            value={departure}
            onChange={(e) => setDeparture(e.target.value)}
            required
          />
        </div>

        <div className="field">
          <label htmlFor="search-adults">Adults</label>
          <select
            id="search-adults"
            name="adults"
            value={adults}
            onChange={(e) => setAdults(Number(e.target.value))}
          >
            {[1, 2, 3, 4, 5, 6].map((n) => (
              <option key={n} value={n}>
                {n} {n === 1 ? "Adult" : "Adults"}
              </option>
            ))}
          </select>
        </div>

        <div className="field">
          <label htmlFor="search-children">Children (2–15)</label>
          <select
            id="search-children"
            name="children"
            value={children}
            onChange={(e) => setChildren(Number(e.target.value))}
          >
            {[0, 1, 2, 3, 4].map((n) => (
              <option key={n} value={n}>
                {n} {n === 1 ? "Child" : "Children"}
              </option>
            ))}
          </select>
        </div>

        <div className="field">
          <label htmlFor="search-infants">Infants (under 2)</label>
          <select
            id="search-infants"
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
          className="btn btn-accent"
          style={compact ? { gridColumn: "1 / -1" } : undefined}
        >
          Check Availability
        </button>
      </div>
      {error && (
        <p role="alert" style={{ color: "var(--status-err-fg)", margin: "0.65rem 0 0", fontSize: "0.88rem" }}>
          {error}
        </p>
      )}
    </form>
  );
}

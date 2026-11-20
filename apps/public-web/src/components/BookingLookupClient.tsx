"use client";

import { useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";

export function BookingLookupClient({ portalEnabled }: { portalEnabled: boolean }) {
  const router = useRouter();
  const [ref, setRef] = useState("");
  const [token, setToken] = useState("");

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const r = ref.trim();
    const t = token.trim();
    if (!r || !t) return;
    try {
      sessionStorage.setItem(`lv_token_${r}`, t);
      sessionStorage.setItem(`lv_token_${r.toUpperCase()}`, t);
    } catch {
      // Fallback handled on confirmation page if storage is unavailable
    }
    router.push(`/booking/confirmation?ref=${encodeURIComponent(r)}`);
  }

  return (
    <div className="card-panel">
      <h1 className="section-title">Retrieve Your Booking</h1>
      <p style={{ color: "var(--text-muted)" }}>
        Enter your reservation or hold reference (e.g. <code>LV-2026-...</code> or{" "}
        <code>HOLD-...</code>) together with the private access token shown during checkout.
        Tokens are never placed in the URL.
      </p>

      <form onSubmit={onSubmit} style={{ display: "grid", gap: "1rem" }}>
        <div className="field">
          <label htmlFor="lookup-ref">Booking or Hold Reference</label>
          <input
            id="lookup-ref"
            type="text"
            required
            value={ref}
            onChange={(e) => setRef(e.target.value)}
            placeholder="e.g. LV-2026-AB12CD"
          />
        </div>

        <div className="field">
          <label htmlFor="lookup-token">Confirmation Security Token</label>
          <input
            id="lookup-token"
            type="password"
            autoComplete="off"
            required
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="Paste your booking security token"
          />
        </div>

        <button type="submit" className="btn btn-primary">
          View Booking Status
        </button>
      </form>

      {portalEnabled ? (
        <p
          style={{
            marginTop: "1.25rem",
            marginBottom: 0,
            fontSize: "0.92rem",
            color: "var(--text-muted)",
          }}
        >
          Don&apos;t have your confirmation token handy?{" "}
          <Link href="/portal" style={{ fontWeight: 600 }}>
            Sign in via the Guest Portal with your email address
          </Link>
          .
        </p>
      ) : null}
    </div>
  );
}

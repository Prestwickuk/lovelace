"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";

export default function BookingLookupPage() {
  const router = useRouter();
  const [ref, setRef] = useState("");
  const [token, setToken] = useState("");

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const r = ref.trim();
    const t = token.trim();
    if (!r || !t) return;
    try {
      sessionStorage.setItem(`tb_token_${r}`, t);
      sessionStorage.setItem(`tb_token_${r.toUpperCase()}`, t);
    } catch {
      // Fallback handled on confirmation page if storage is unavailable
    }
    router.push(`/booking/confirmation?ref=${encodeURIComponent(r)}`);
  }

  return (
    <>
      <SiteHeader />
      <main className="container section" style={{ maxWidth: "560px" }}>
        <div className="card-panel">
          <h1 className="section-title">Retrieve Your Booking</h1>
          <p style={{ color: "var(--text-muted)" }}>
            Enter your reservation or hold reference (e.g.{" "}
            <code>TB-2026-...</code> or <code>HOLD-...</code>) together with the
            private access token shown during checkout. Tokens are never placed in
            the URL.
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
                placeholder="e.g. TB-2026-AB12CD"
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
        </div>
      </main>
      <SiteFooter />
    </>
  );
}

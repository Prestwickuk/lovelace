"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import { buildSafeSandboxRedirect, formatMoney } from "@/lib/format";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";

function SandboxCheckoutContent() {
  const sp = useSearchParams();
  const sessionId = sp.get("session_id") || "";
  const holdRef = sp.get("hold_ref") || "";
  const amountPence = Number(sp.get("amount_pence") || 0) || 0;
  const currency = sp.get("currency") || "GBP";
  const description = sp.get("description") || "Direct Stay Deposit";

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function triggerOutcome(outcome: "paid" | "failed") {
    setBusy(true);
    setError("");
    try {
      const res = await fetch("/api/public/dev/stripe-sandbox/complete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ session_id: sessionId, outcome }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.error?.message || "Sandbox webhook dispatch failed.");
        setBusy(false);
        return;
      }
      window.location.href = buildSafeSandboxRedirect(outcome, holdRef);
    } catch {
      setError("Network error completing sandbox payment.");
      setBusy(false);
    }
  }

  return (
    <main className="container section" style={{ maxWidth: "640px" }}>
      <div className="card-panel">
        <span className="badge badge-warn">
          Stripe Test Sandbox Simulator (Development / QA Only)
        </span>
        <h1 className="section-title" style={{ marginTop: "0.75rem" }}>
          Authorise Card Payment
        </h1>
        <p style={{ color: "var(--text-muted)" }}>
          In production, guests complete payment directly on Stripe&apos;s hosted
          Checkout page. This local non-production simulator fires a genuine
          HMAC-SHA256 signed Stripe webhook event to verify the full end-to-end
          booking lifecycle.
        </p>

        <div
          style={{
            background: "var(--bg-page)",
            border: "1px solid var(--border-subtle)",
            borderRadius: "var(--radius-sm)",
            padding: "1rem",
            marginBlock: "1rem",
          }}
        >
          <div>
            <strong>Item:</strong> {description}
          </div>
          <div>
            <strong>Hold Reference:</strong> <code>{holdRef}</code>
          </div>
          <div style={{ fontSize: "1.25rem", marginTop: "0.35rem", color: "var(--brand-forest)" }}>
            <strong>Amount to Authorise:</strong>{" "}
            {formatMoney(amountPence, currency)}
          </div>
        </div>

        {error && (
          <div className="alert alert-err" role="alert">
            {error}
          </div>
        )}

        <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap" }}>
          <button
            type="button"
            className="btn btn-accent"
            disabled={busy || !sessionId}
            onClick={() => triggerOutcome("paid")}
          >
            {busy ? "Processing Webhook..." : "Complete Sandbox Payment"}
          </button>

          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || !sessionId}
            onClick={() => triggerOutcome("failed")}
          >
            Simulate Card Decline
          </button>

          <Link href="/book?cancelled=1" className="btn btn-danger-outline">
            Cancel &amp; Return
          </Link>
        </div>
      </div>
    </main>
  );
}

export function SandboxCheckoutClient() {
  return (
    <>
      <SiteHeader />
      <Suspense fallback={<div className="container section">Loading checkout...</div>}>
        <SandboxCheckoutContent />
      </Suspense>
      <SiteFooter />
    </>
  );
}

import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { expect, test } from "@playwright/test";

const PSQL_BIN =
  process.env.PSQL_BIN ||
  (existsSync("/opt/homebrew/bin/psql") ? "/opt/homebrew/bin/psql" : "psql");
const PUBLIC_ORIGIN = (process.env.E2E_BASE_URL || "http://127.0.0.1:3000").replace(/\/+$/, "");
const E2E_DB_URL =
  process.env.E2E_DATABASE_URL ||
  "postgres://lovelace_migrator:ci-only-migrator-password@127.0.0.1:55432/lovelace_m4_e2e_test?sslmode=disable";

function runSql(sql: string): string {
  return execFileSync(
    PSQL_BIN,
    [E2E_DB_URL, "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql],
    { encoding: "utf8" },
  ).trim();
}

function futureDate(daysAhead: number): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + daysAhead);
  return d.toISOString().slice(0, 10);
}

function extractChallengeTokens(recipientEmail: string): {
  otp: string;
  magicToken: string;
} {
  const safeEmail = recipientEmail.replace(/'/g, "''").toLowerCase();
  const body = runSql(
    `SELECT body_text FROM email_outbox WHERE LOWER(recipient_email) = '${safeEmail}' ORDER BY created_at DESC LIMIT 1`,
  );
  const otpMatch = body.match(/\b(\d{6})\b/);
  const magicMatch = body.match(/#t=([0-9a-f]{64})/i);
  if (!otpMatch) {
    throw new Error(`Expected 6-digit OTP in email_outbox for ${recipientEmail}, got: ${body}`);
  }
  return {
    otp: otpMatch[1],
    magicToken: magicMatch ? magicMatch[1] : "",
  };
}

test.describe.serial("Phase 3 Milestone 4 — Self-Service Public Guest Portal", () => {
  test("disabled by default hides portal nav, renders safe notice, and returns 404 on portal API", async ({
    page,
    request,
  }) => {
    runSql(
      `UPDATE property_booking_policies SET guest_portal_enabled = FALSE WHERE property_id = (SELECT id FROM properties WHERE slug = 'demo-property')`,
    );

    await page.goto("/");
    await expect(
      page.getByRole("navigation", { name: "Main navigation" }).getByRole("link", {
        name: "Guest Portal",
      }),
    ).toHaveCount(0);

    const portalResp = await page.goto("/portal");
    expect(portalResp?.headers()["cache-control"]).toContain("no-store");
    expect(portalResp?.headers()["referrer-policy"]).toBe("no-referrer");
    expect(portalResp?.headers()["x-robots-tag"]).toContain("noindex");

    await expect(
      page.getByText("Online Guest Portal sign-in is not currently enabled for this property"),
    ).toBeVisible();

    const chResp = await request.post("/api/public/portal/auth/challenge", {
      headers: { Origin: PUBLIC_ORIGIN },
      data: { email: "pilot.guest@example.com", purpose: "LOGIN" },
    });
    expect(chResp.status()).toBe(404);
    expect(chResp.headers()["cache-control"]).toContain("no-store");
    expect(chResp.headers()["referrer-policy"]).toBe("no-referrer");
  });

  test("BFF enforces query-secret ban, Origin/X-Forwarded-Host spoofing checks, and admin cookie separation", async ({
    request,
  }) => {
    runSql(
      `UPDATE property_booking_policies SET guest_portal_enabled = TRUE WHERE property_id = (SELECT id FROM properties WHERE slug = 'demo-property')`,
    );

    const queryTokResp = await request.get(
      "/api/public/portal/bookings?token=leaked-query-secret",
    );
    expect(queryTokResp.status()).toBe(400);
    expect(await queryTokResp.text()).toContain("query_token_forbidden");

    const evilOriginResp = await request.post("/api/public/portal/auth/challenge", {
      headers: { Origin: "https://evil.example.org" },
      data: { email: "pilot.guest@example.com" },
    });
    expect(evilOriginResp.status()).toBe(403);

    const spoofedForwardedHostResp = await request.post(
      "/api/public/portal/auth/challenge",
      {
        headers: {
          Origin: "https://evil.example.org",
          "X-Forwarded-Host": "evil.example.org",
          "X-Forwarded-Proto": "https",
        },
        data: { email: "pilot.guest@example.com" },
      },
    );
    expect(spoofedForwardedHostResp.status()).toBe(403);

    const spoofedHostHeaderResp = await request.post(
      "/api/public/portal/auth/challenge",
      {
        headers: {
          Host: "evil.example.org",
          Origin: "http://evil.example.org",
        },
        data: { email: "pilot.guest@example.com" },
      },
    );
    expect(spoofedHostHeaderResp.status()).toBe(403);

    const adminCookieResp = await request.get("/api/public/portal/bookings", {
      headers: {
        Cookie: "lovelace_session=forged-admin-session-token",
      },
    });
    expect(adminCookieResp.status()).toBe(401);
  });

  test("end-to-end Tier A & Tier B portal flows, OTA read-only, privacy gates, keyboard focus, and 375px/200% zoom", async ({
    browser,
    page,
    request,
  }) => {
    const consoleErrors: string[] = [];
    const pageErrors: string[] = [];
    const server5xx: string[] = [];

    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => pageErrors.push(String(err)));
    page.on("response", (res) => {
      if (res.status() >= 500) server5xx.push(`${res.status()} ${res.url()}`);
    });

    runSql(
      `UPDATE property_booking_policies SET guest_portal_enabled = TRUE WHERE property_id = (SELECT id FROM properties WHERE slug = 'demo-property')`,
    );

    // 1. Create a genuine confirmed direct booking via public BFF + sandbox checkout.
    const uniqueEmail = `ceryn.portal.${Date.now()}@example.com`;
    const arr = futureDate(95);
    const dep = futureDate(97);
    let unitCode = "meadow-pitch-ehu";

    const availRes = await request.post("/api/public/availability", {
      data: {
        arrival_date: arr,
        departure_date: dep,
        adults: 2,
        children: 0,
        infants: 0,
      },
    });
    expect(availRes.ok()).toBeTruthy();
    const availJson = (await availRes.json()) as {
      items: Array<{
        unit_type: { code: string };
        available_units: number;
        meets_stay_rules: boolean;
      }>;
    };
    const openItem = availJson.items.find(
      (i) => i.available_units > 0 && i.meets_stay_rules,
    );
    expect(openItem).toBeTruthy();
    unitCode = openItem!.unit_type.code;

    const holdRes = await request.post("/api/public/booking/holds", {
      headers: { Origin: PUBLIC_ORIGIN },
      data: {
        unit_type_code: unitCode,
        arrival_date: arr,
        departure_date: dep,
        adults: 2,
        children: 0,
        infants: 0,
        extras: [],
        guest_name: "Ceryn Llewellyn",
        guest_email: uniqueEmail,
        guest_phone: "+447700900444",
        accept_terms: true,
      },
    });
    expect(holdRes.ok()).toBeTruthy();
    const holdJson = (await holdRes.json()) as {
      hold: { id: string; hold_reference: string };
      confirmation_token: string;
    };

    const checkoutRes = await request.post("/api/public/booking/checkout", {
      headers: { Origin: PUBLIC_ORIGIN },
      data: { hold_id: holdJson.hold.id },
    });
    expect(checkoutRes.ok()).toBeTruthy();
    const checkoutJson = (await checkoutRes.json()) as { checkout_url: string };
    const checkoutUrl = new URL(checkoutJson.checkout_url, PUBLIC_ORIGIN);
    const sessionId = checkoutUrl.searchParams.get("session_id") || "";
    expect(sessionId).not.toBe("");

    const completeRes = await request.post("/api/public/dev/stripe-sandbox/complete", {
      headers: { Origin: PUBLIC_ORIGIN },
      data: {
        session_id: sessionId,
        outcome: "paid",
      },
    });
    expect(completeRes.ok()).toBeTruthy();

    const lookupRes = await request.post("/api/public/booking/confirmation", {
      headers: {
        Origin: PUBLIC_ORIGIN,
        Authorization: `Bearer ${holdJson.confirmation_token}`,
      },
      data: {
        booking_reference: holdJson.hold.hold_reference,
      },
    });
    expect(lookupRes.ok()).toBeTruthy();
    const confJson = (await lookupRes.json()) as {
      reservation_reference: string;
    };
    const directRef = confJson.reservation_reference;
    expect(directRef).toMatch(/^LV-/);

    // Seed an OTA (AIRBNB) stay and internal CRM note on the same canonical guest to verify D4 & DTO redaction.
    const otaRef = `LV-OTA-${Date.now().toString().slice(-6)}`;
    const safeEmailSql = uniqueEmail.replace(/'/g, "''");
    runSql(`
      DO $$
      DECLARE
        v_prop_id UUID;
        v_guest_id UUID;
        v_admin_id UUID;
        v_res_id UUID := gen_random_uuid();
      BEGIN
        SELECT id INTO v_prop_id FROM properties WHERE slug = 'demo-property';
        SELECT guest_id INTO v_guest_id FROM reservations WHERE property_id = v_prop_id AND reference = '${directRef}';
        SELECT id INTO v_admin_id FROM admin_users WHERE property_id = v_prop_id LIMIT 1;

        UPDATE reservations SET internal_notes = 'STAFF_SECRET_INTERNAL_RES_NOTE' WHERE property_id = v_prop_id AND reference = '${directRef}';
        INSERT INTO guest_notes (id, property_id, guest_id, category, content, is_pinned, created_by, updated_by)
        VALUES (gen_random_uuid(), v_prop_id, v_guest_id, 'OPERATIONAL', 'STAFF_SECRET_CRM_WHISPER', TRUE, v_admin_id, v_admin_id);

        INSERT INTO booking_channels (id, property_id, code, name, category, is_active, sort_order)
        VALUES (gen_random_uuid(), v_prop_id, 'AIRBNB', 'Airbnb', 'ota', TRUE, 10)
        ON CONFLICT (property_id, code) DO NOTHING;

        INSERT INTO reservations (
          id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
          channel_code, stay_range, adults, children, infants, status,
          currency, total_pence, deposit_pence, amount_paid_pence, payment_status,
          internal_notes
        ) VALUES (
          v_res_id, v_prop_id, '${otaRef}', v_guest_id, 'Ceryn Llewellyn', '${safeEmailSql}', '+447700900444',
          'AIRBNB', daterange((CURRENT_DATE + 120), (CURRENT_DATE + 123), '[)'), 2, 0, 0, 'CONFIRMED',
          'GBP', 48000, 0, 48000, 'PAID',
          'STAFF_SECRET_OTA_NOTE'
        );
      END $$;
    `);

    // 2. Verify "Guest Portal" link now appears in main site navigation when pilot-enabled.
    await page.goto("/");
    await expect(
      page.getByRole("navigation", { name: "Main navigation" }).getByRole("link", {
        name: "Guest Portal",
      }),
    ).toBeVisible();

    // 3. Tier A Single-Booking Token Access (D1): isolated to directRef only!
    await page.goto("/portal");
    await page.keyboard.press("Tab");
    const activeFocusOutline = await page.evaluate(() => {
      const el = document.activeElement;
      return el ? window.getComputedStyle(el).outlineStyle : "none";
    });
    expect(activeFocusOutline).not.toBe("none");

    await page.getByRole("tab", { name: "Single Booking Token" }).click();
    await page.fill("#portal-token-ref", directRef);
    await page.fill("#portal-token-secret", holdJson.confirmation_token);
    await page.getByRole("button", { name: "Access Single Booking" }).click();

    await expect(page).toHaveURL(new RegExp(`/portal/bookings/${directRef}`));
    await expect(page.getByText("Single-Booking Access")).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Profile & Enquiries" }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("link", { name: "Consent & Privacy" }),
    ).toHaveCount(0);

    // Confirm zero portal credentials stored in localStorage or sessionStorage.
    const clientStorageKeys = await page.evaluate(() => [
      ...Object.keys(window.localStorage),
      ...Object.keys(window.sessionStorage),
    ]);
    expect(clientStorageKeys).toEqual([]);

    // Update Arrival ETA & Notes in Tier A session.
    await page.fill("#booking-eta", "16:45");
    await page.fill("#booking-arrival-notes", "Arriving with 1 border collie");
    await page.getByRole("button", { name: "Save Arrival Details" }).click();
    await expect(page.getByText("Arrival details updated.")).toBeVisible();

    // Sign out of Tier A session.
    await page.getByRole("button", { name: "Sign Out" }).click();
    await expect(page).toHaveURL(/\/portal$/);

    // 4. Tier B Passwordless Magic-Link Same-Browser Redemption (D1) & Cross-Browser Rejection!
    await page.fill("#portal-email", uniqueEmail);
    await page
      .getByRole("button", { name: "Send 6-Digit Verification Code" })
      .click();
    await expect(page.locator("#portal-verify-code")).toBeVisible();

    const { magicToken } = extractChallengeTokens(uniqueEmail);
    expect(magicToken).toHaveLength(64);

    // Attempt magic-link redemption in a DIFFERENT browser context (missing lovelace_portal_nonce cookie) -> rejected & falls back to 6-digit code!
    const otherContext = await browser.newContext();
    const otherPage = await otherContext.newPage();
    await otherPage.goto(`/portal/verify#t=${magicToken}`);
    await otherPage.getByRole("button", { name: "Complete Sign-In" }).click();
    await expect(otherPage.locator("#verify-fallback-code")).toBeVisible();
    await otherContext.close();

    // Redeem magic link in the SAME browser context -> scrubs #t= fragment and signs into Tier B!
    await page.goto(`/portal/verify#t=${magicToken}`);
    await expect
      .poll(() => page.evaluate(() => window.location.hash))
      .toBe("");
    await page.getByRole("button", { name: "Complete Sign-In" }).click();

    await expect(page).toHaveURL(/\/portal\/bookings$/);
    await expect(page.getByText("Verified Guest Account")).toBeVisible();
    await expect(page.getByText(directRef)).toBeVisible();
    await expect(page.getByText(otaRef)).toBeVisible();
    await expect(page.getByText(/Managed & billed via AIRBNB/)).toBeVisible();

    // Verify internal staff notes NEVER appear in the rendered DOM.
    const bodyText = await page.locator("body").innerText();
    expect(bodyText).not.toContain("STAFF_SECRET_INTERNAL_RES_NOTE");
    expect(bodyText).not.toContain("STAFF_SECRET_CRM_WHISPER");
    expect(bodyText).not.toContain("STAFF_SECRET_OTA_NOTE");

    // 5. Responsive 375px mobile & 200% zoom (640px) zero horizontal overflow across portal views!
    for (const vp of [
      { width: 375, height: 812 },
      { width: 640, height: 720 },
    ]) {
      await page.setViewportSize(vp);
      const hasOverflow = await page.evaluate(
        () => document.documentElement.scrollWidth > window.innerWidth + 1,
      );
      expect(hasOverflow).toBeFalsy();
    }
    await page.setViewportSize({ width: 1280, height: 800 });

    // 6. Inspect OTA booking detail: read-only notice, no arrival/change/cancel mutation forms (D4).
    await page.goto(`/portal/bookings/${otaRef}`);
    await expect(page.getByText(/Managed & billed via AIRBNB/)).toBeVisible();
    await expect(page.locator("#booking-eta")).toHaveCount(0);
    await expect(page.locator("#chg-notes")).toHaveCount(0);
    await expect(page.locator("#cancel-note")).toHaveCount(0);

    // 7. Submit request-only booking change & cancellation request on direct booking (D5).
    await page.goto(`/portal/bookings/${directRef}`);
    await page.fill("#chg-notes", "Could we extend departure by 1 extra night if free?");
    await page.getByRole("button", { name: "Submit Change Request" }).click();
    await expect(
      page.getByText(/Booking change request submitted \(ENQ-/),
    ).toBeVisible();

    await page.fill("#cancel-note", "Plans changed, requesting team review");
    await page
      .getByRole("button", { name: "Submit Cancellation Request" })
      .click();
    await expect(
      page.getByText("Cancellation request recorded for review by our reservations team."),
    ).toBeVisible();

    // 8. Profile, language preference readiness (D10), Stay Preferences, & Support Thread.
    await page.getByRole("link", { name: "Profile & Enquiries" }).click();
    await expect(page).toHaveURL(/\/portal\/profile$/);
    await page.selectOption("#prof-lang", "fr");
    await page.fill("#pref-key", "dogs");
    await page.fill("#pref-val", "1 border collie");
    await page.getByRole("button", { name: "Add Stay Preference" }).click();
    await page
      .getByRole("button", { name: "Save Contact & Stay Preferences" })
      .click();
    await expect(
      page.getByText("Your contact and stay preferences have been updated."),
    ).toBeVisible();

    await page.fill("#enq-subject", "Gate code for Friday evening");
    await page.fill("#enq-body", "What time does the barrier close on Friday?");
    await page.getByRole("button", { name: "Send Enquiry" }).click();
    await expect(page.getByText("Gate code for Friday evening")).toBeVisible();

    // 9. Consent & Privacy Rights (D6, D7, D8, Section 7.2 uniform receipt without status disclosure).
    await page.getByRole("link", { name: "Consent & Privacy" }).click();
    await expect(page).toHaveURL(/\/portal\/privacy$/);
    await expect(page.locator("body")).not.toContainText("UK GDPR Art.");

    await page
      .getByRole("button", { name: "Opt In to Seasonal Updates" })
      .click();
    await expect(
      page.getByText("Marketing email consent granted and recorded."),
    ).toBeVisible();

    await page
      .getByRole("button", { name: "Withdraw Marketing Consent" })
      .click();
    await expect(page.getByText(/Marketing email consent withdrawn/)).toBeVisible();

    await page.fill("#sar-notes", "Booking correspondence summary");
    await page
      .getByRole("button", { name: "Submit Data Access Request" })
      .click();
    await expect(
      page.getByText(
        "Your request has been recorded for review by the property team.",
      ),
    ).toBeVisible();
    await expect(page.locator("body")).not.toContainText("ENQ-");

    await page.fill("#erasure-reason", "No longer travelling");
    await page
      .getByLabel(/I understand that once personal data erasure is completed/)
      .check();
    await page.getByRole("button", { name: "Submit Erasure Request" }).click();
    await expect(
      page.getByText(
        "Your request has been recorded for review by the property team.",
      ),
    ).toBeVisible();
    await expect(page.locator("body")).not.toContainText("ENQ-");

    // Verify SAR & Erasure requests are NOT surfaced in /portal/profile support threads (Section 7.2).
    await page.getByRole("link", { name: "Profile & Enquiries" }).click();
    await expect(page.getByText("Gate code for Friday evening")).toBeVisible();
    await expect(page.locator("body")).not.toContainText("Privacy Rights");
    await expect(page.locator("body")).not.toContainText(
      "Booking correspondence summary",
    );

    // Reset property policy back to disabled-by-default after E2E suite completes.
    runSql(
      `UPDATE property_booking_policies SET guest_portal_enabled = FALSE WHERE property_id = (SELECT id FROM properties WHERE slug = 'demo-property')`,
    );

    expect(consoleErrors).toEqual([]);
    expect(pageErrors).toEqual([]);
    expect(server5xx).toEqual([]);
  });
});

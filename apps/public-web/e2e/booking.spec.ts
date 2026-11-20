import { expect, test } from "@playwright/test";

function futureDate(daysAhead: number): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + daysAhead);
  return d.toISOString().slice(0, 10);
}

test.describe("Tir Bach Public Direct Booking Engine", () => {
  test("homepage shows Tir Bach identity, strict CSP headers, and separates admin surface", async ({
    page,
  }) => {
    const homeResp = await page.goto("/");
    expect(homeResp?.headers()["content-security-policy"]).toContain("default-src 'self'");
    expect(homeResp?.headers()["content-security-policy"]).toContain("frame-ancestors 'none'");
    expect(homeResp?.headers()["x-frame-options"]).toBe("DENY");
    expect(homeResp?.headers()["x-content-type-options"]).toBe("nosniff");
    await expect(page.getByRole("heading", { level: 1 })).toContainText(
      "Tir Bach Campsite",
    );
    await expect(page.locator("footer")).toContainText(
      "Booking system powered by LOVELACE",
    );
    await expect(page.locator("body")).not.toContainText("Channel Manager");
    await expect(page.locator("body")).not.toContainText("Admin Console");
    await expect(page.locator("body")).not.toContainText("+44 1437 555 019");
  });

  test("sandbox checkout ignores external success_url and cancel_url redirect attempts", async ({
    page,
    request,
  }) => {
    // Attempt BFF checkout with client-supplied redirect URL -> rejected 422
    const maliciousCheckout = await request.post("/api/public/booking/checkout", {
      data: {
        hold_id: "00000000-0000-0000-0000-000000000001",
        success_url: "https://evil.example/phish",
      },
    });
    expect(maliciousCheckout.status()).toBe(422);

    // Sandbox page ignores attacker query success_url/cancel_url
    await page.goto(
      "/checkout/sandbox?session_id=cs_fake&hold_ref=HLD-2026-TEST&success_url=https://evil.example/steal&cancel_url=https://evil.example/cancel",
    );
    const cancelLink = page.getByRole("link", { name: "Cancel & Return" });
    await expect(cancelLink).toHaveAttribute("href", "/book?cancelled=1");
  });

  test("responsive mobile layout has zero horizontal overflow", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/");
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    );
    expect(overflow).toBeFalsy();
  });

  test("embeddable /embed/[slug] widget allows external HTTPS framing while non-embed routes stay DENY", async ({
    page,
  }) => {
    const arr = futureDate(84);
    const dep = futureDate(86);
    const embedResp = await page.goto(
      `/embed/tir-bach?arrival=${arr}&departure=${dep}&adults=2`,
    );
    expect(embedResp?.status()).toBe(200);
    expect(embedResp?.headers()["x-frame-options"]).toBeUndefined();
    expect(embedResp?.headers()["content-security-policy"]).toContain(
      "frame-ancestors https:",
    );
    expect(embedResp?.headers()["x-content-type-options"]).toBe("nosniff");

    await expect(
      page.getByRole("heading", { level: 1, name: /Tir Bach/i }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Select & Book" }).first(),
    ).toBeVisible();

    await page.getByRole("button", { name: "Select & Book" }).first().click();
    await expect(
      page.getByRole("button", { name: "Hold Dates & Review Payment" }),
    ).toBeVisible({ timeout: 10_000 });
    await expect(
      page.getByRole("button", {
        name: /Choose Different Dates or Accommodation/i,
      }),
    ).toBeVisible();
  });

  test("complete end-to-end search -> hold -> release -> re-hold -> sandbox Stripe -> confirmation -> cancel request", async ({
    page,
    request,
  }) => {
    const consoleErrors: string[] = [];
    const pageErrors: string[] = [];
    const server5xx: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => pageErrors.push(err.message));
    page.on("response", (res) => {
      if (res.status() >= 500) server5xx.push(`${res.status()} ${res.url()}`);
    });

    // Pick an open 2-night window for the single-unit Preseli Shepherd's Hut so consecutive runs succeed reliably.
    let arr = futureDate(70);
    let dep = futureDate(72);
    for (let offset = 70; offset <= 160; offset += 4) {
      const candidateArr = futureDate(offset);
      const candidateDep = futureDate(offset + 2);
      const availRes = await request.post("/api/public/availability", {
        data: {
          arrival_date: candidateArr,
          departure_date: candidateDep,
          adults: 2,
          children: 0,
          infants: 0,
        },
      });
      if (availRes.ok()) {
        const availJson = (await availRes.json()) as {
          items?: Array<{ unit_type?: { code?: string }; available_units?: number }>;
        };
        const hut = availJson.items?.find(
          (r) => r.unit_type?.code === "shepherds-hut" && (r.available_units || 0) > 0,
        );
        if (hut) {
          arr = candidateArr;
          dep = candidateDep;
          break;
        }
      }
    }

    // 1. Search live availability from homepage
    await page.goto("/");
    await page.fill("#search-arrival", arr);
    await page.fill("#search-departure", dep);
    await page.getByRole("button", { name: "Check Availability" }).click();

    await expect(page).toHaveURL(new RegExp(`/stays\\?.*arrival=${arr}`));
    await expect(page.getByText("Preseli Shepherd's Hut")).toBeVisible();

    // 2. Navigate to Shepherd's Hut detail page & continue to booking
    await page
      .locator("article", { hasText: "Preseli Shepherd's Hut" })
      .getByRole("link", { name: "Book Now" })
      .click();

    await expect(page).toHaveURL(/\/book\?unit_type=shepherds-hut/);
    await expect(page.getByText("£250.00").first()).toBeVisible(); // 2 nights * £125

    // 3. Add Seasoned Firewood Basket extra -> authoritative backend pricing updates total to £262.00
    const firstExtraSelect = page.locator("select[id^='extra-']").first();
    await firstExtraSelect.selectOption("1");
    await expect(page.getByText("£262.00").first()).toBeVisible({ timeout: 10_000 });

    // 4. Fill guest details & create temporary hold
    await page.fill("#guest_name", "Dafydd Owen");
    await page.fill("#guest_email", "dafydd@example.wales");
    await page.fill("#guest_phone", "+447700900777");
    await page.fill("#special_requests", "Arriving around 4pm");
    await page.check("#accept_terms");
    await page.getByRole("button", { name: "Hold Dates & Review Payment" }).click();

    await expect(page.getByText("Dates Temporarily Reserved")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText(/Inventory Held for \d{2}:\d{2}/)).toBeVisible();

    // 5. Explicitly release hold & verify we return to editable details
    await page.getByRole("button", { name: "Release Hold & Edit Details" }).click();
    await expect(
      page.getByRole("button", { name: "Hold Dates & Review Payment" }),
    ).toBeVisible();

    // 6. Re-create hold & complete Stripe Sandbox checkout (triggers genuine signed webhook!)
    await page.fill("#guest_name", "Dafydd Owen");
    await page.fill("#guest_email", "dafydd@example.wales");
    await page.fill("#guest_phone", "+447700900777");
    await page.check("#accept_terms");
    await page.getByRole("button", { name: "Hold Dates & Review Payment" }).click();

    await expect(page.getByText("Dates Temporarily Reserved")).toBeVisible();
    await page.getByRole("button", { name: /Pay £\d+\.\d{2} Securely/ }).click();

    await expect(page).toHaveURL(/\/checkout\/sandbox/);
    await page.getByRole("button", { name: "Complete Sandbox Payment" }).click();

    // 7. Confirmation page shows CONFIRMED + PARTIALLY_PAID (25% deposit £65.50) + itemized snapshot + cancellation request!
    await expect(page).toHaveURL(/\/booking\/confirmation/, { timeout: 15_000 });
    expect(page.url()).not.toContain("token=");
    await expect(page.getByText("Status: CONFIRMED")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(/Payment: (PAID|PARTIALLY_PAID)/)).toBeVisible();
    await expect(page.getByText("£262.00")).toBeVisible();
    await expect(page.getByText("£65.50")).toBeVisible();
    await expect(page.getByText("Dafydd Owen")).toBeVisible();

    // Submit guest cancellation request
    await page.fill("#cancel_note", "Flight delayed, need to cancel");
    await page.getByRole("button", { name: "Request Cancellation" }).click();
    await expect(
      page.getByText("Your cancellation request has been logged for our team"),
    ).toBeVisible({ timeout: 10_000 });

    // 8. Verify Preseli Shepherd's Hut (only 1 physical unit!) is now unavailable for same dates!
    await page.goto(`/stays?arrival=${arr}&departure=${dep}&adults=2`);
    await expect(page.getByText("Preseli Shepherd's Hut")).toHaveCount(0);

    expect(consoleErrors).toEqual([]);
    expect(pageErrors).toEqual([]);
    expect(server5xx).toEqual([]);
  });
});

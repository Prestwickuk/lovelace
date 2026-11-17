import { describe, expect, it } from "vitest";
import {
  buildSafeSandboxRedirect,
  formatAmenity,
  formatCategoryLabel,
  formatMoney,
  formatPricingModel,
  formatStayDate,
  getUnitTypeGallery,
  isSafeCheckoutRedirectUrl,
} from "./format";

describe("format utilities", () => {
  it("formats GBP minor units accurately", () => {
    expect(formatMoney(9500, "GBP")).toBe("£95.00");
    expect(formatMoney(12550, "GBP")).toBe("£125.50");
    expect(formatMoney(-500, "GBP")).toBe("-£5.00");
    expect(formatMoney(4200, "EUR")).toBe("€42.00");
  });

  it("formats dates and category/pricing labels across uppercase and Go lowercase API enums", () => {
    expect(formatStayDate("2026-07-15")).toContain("2026");
    expect(formatCategoryLabel("GLAMPING")).toBe("Glamping");
    expect(formatCategoryLabel("tent_pitch")).toBe("Tent Pitch");
    expect(formatCategoryLabel("touring_pitch")).toBe("Touring & Hook-Up");
    expect(formatPricingModel("PER_NIGHT")).toBe("per night");
    expect(formatPricingModel("per_night")).toBe("per night");
    expect(formatPricingModel("per_person_per_night")).toBe("per guest per night");
    expect(formatAmenity("wood-burner")).toBe("Wood Burner");
  });

  it("returns local fallback image for known Tir Bach unit type codes", () => {
    const imgs = getUnitTypeGallery({
      id: "1",
      code: "shepherds-hut",
      name: "Preseli Shepherd's Hut",
      category: "CABIN",
      description: "",
      base_capacity_adults: 2,
      max_adults: 2,
      max_children: 0,
      max_infants: 1,
      max_guests: 2,
      pets_allowed: false,
      max_pets: 0,
      base_nightly_rate_pence: 12500,
      tax_rate_bps: 0,
      min_nights: 2,
      max_nights: 14,
      amenities: [],
      images: [],
    });
    expect(imgs[0].url).toBe("/images/shepherds-hut.jpg");
  });

  it("derives same-origin sandbox redirects and sanitizes hold references", () => {
    expect(buildSafeSandboxRedirect("paid", "HLD-2026-AB12")).toBe(
      "/booking/confirmation?ref=HLD-2026-AB12",
    );
    expect(buildSafeSandboxRedirect("paid", "https://evil.example/steal?x=1")).toBe(
      "/booking/confirmation?ref=httpsevilexamplestealx1",
    );
    expect(buildSafeSandboxRedirect("paid", "")).toBe("/booking/confirmation");
    expect(buildSafeSandboxRedirect("failed", "HLD-2026-AB12")).toBe(
      "/book?cancelled=1",
    );
  });

  it("allows only /checkout/sandbox and https://checkout.stripe.com checkout redirect URLs", () => {
    expect(isSafeCheckoutRedirectUrl("/checkout/sandbox?session_id=cs_sandbox_1")).toBe(
      true,
    );
    expect(
      isSafeCheckoutRedirectUrl(
        "http://127.0.0.1:3000/checkout/sandbox?session_id=cs_sandbox_1",
        "http://127.0.0.1:3000",
      ),
    ).toBe(true);
    expect(
      isSafeCheckoutRedirectUrl("https://checkout.stripe.com/c/pay/cs_test_123"),
    ).toBe(true);
    expect(isSafeCheckoutRedirectUrl("http://checkout.stripe.com/c/pay/cs_test_123")).toBe(
      false,
    );
    expect(
      isSafeCheckoutRedirectUrl("https://user@checkout.stripe.com/c/pay/cs_test_123"),
    ).toBe(false);
    expect(isSafeCheckoutRedirectUrl("https://evil.example/phish")).toBe(false);
    expect(isSafeCheckoutRedirectUrl("//evil.example/checkout/sandbox")).toBe(false);
    expect(isSafeCheckoutRedirectUrl("/stays")).toBe(false);
  });
});

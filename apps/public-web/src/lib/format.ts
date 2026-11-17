import type { PublicUnitTypeView, UnitTypeImage } from "./types";

export function formatMoney(pence: number, currency = "GBP"): string {
  const symbol =
    currency === "EUR" ? "€" : currency === "USD" ? "$" : "£";
  const abs = Math.abs(pence);
  const formatted = `${symbol}${(abs / 100).toFixed(2)}`;
  return pence < 0 ? `-${formatted}` : formatted;
}

export function formatStayDate(isoDate: string): string {
  if (!isoDate) return "";
  const parsed = new Date(`${isoDate}T00:00:00Z`);
  if (Number.isNaN(parsed.getTime())) return isoDate;
  return new Intl.DateTimeFormat("en-GB", {
    weekday: "short",
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  }).format(parsed);
}

export function formatPricingModel(model: string): string {
  switch ((model || "").trim().toUpperCase()) {
    case "PER_NIGHT":
      return "per night";
    case "PER_PERSON_PER_STAY":
      return "per guest per stay";
    case "PER_PERSON_PER_NIGHT":
      return "per guest per night";
    default:
      return "per stay";
  }
}

export function formatCategoryLabel(category: string): string {
  switch ((category || "").trim().toUpperCase()) {
    case "GLAMPING":
      return "Glamping";
    case "CABIN":
      return "Cabin & Hut";
    case "TENT_PITCH":
      return "Tent Pitch";
    case "TOURING_PITCH":
      return "Touring & Hook-Up";
    case "OTHER":
      return "Other Accommodation";
    default:
      return category;
  }
}

export function formatAmenity(slug: string): string {
  return slug
    .replaceAll("-", " ")
    .replace(/\b\w/g, (ch) => ch.toUpperCase());
}

const FALLBACK_IMAGES: Record<string, UnitTypeImage> = {
  "bell-tent": {
    url: "/images/bell-tent.jpg",
    alt: "Furnished 5m canvas bell tent on the wildflower meadow at Tir Bach Campsite",
  },
  "shepherds-hut": {
    url: "/images/shepherds-hut.jpg",
    alt: "Preseli handcrafted timber shepherd's hut overlooking the coastal hills",
  },
  "grass-pitch": {
    url: "/images/grass-pitch.jpg",
    alt: "Sheltered meadow grass tent pitch bordered by Welsh stone hedgerows",
  },
  "electric-pitch": {
    url: "/images/electric-pitch.jpg",
    alt: "Level grass and gravel pitch with 16A electric hook-up",
  },
};

export function getUnitTypeGallery(ut: PublicUnitTypeView): UnitTypeImage[] {
  if (Array.isArray(ut.images) && ut.images.length > 0) {
    return ut.images;
  }
  const fallback = FALLBACK_IMAGES[ut.code] ?? {
    url: "/images/hero-pembrokeshire.jpg",
    alt: `${ut.name} at Tir Bach Campsite, Pembrokeshire`,
  };
  return [fallback];
}

export function defaultSearchDates(): { arrival: string; departure: string } {
  const start = new Date();
  start.setUTCDate(start.getUTCDate() + 14);
  const end = new Date(start);
  end.setUTCDate(end.getUTCDate() + 2);
  return {
    arrival: start.toISOString().slice(0, 10),
    departure: end.toISOString().slice(0, 10),
  };
}

/**
 * Derives an approved same-origin route for sandbox completion without ever
 * accepting arbitrary query-parameter redirect URLs.
 */
export function buildSafeSandboxRedirect(
  outcome: "paid" | "failed",
  holdRef: string,
): string {
  if (outcome === "failed") {
    return "/book?cancelled=1";
  }
  const cleanRef = holdRef.trim().replace(/[^A-Za-z0-9_-]/g, "");
  if (!cleanRef) {
    return "/booking/confirmation";
  }
  return `/booking/confirmation?ref=${encodeURIComponent(cleanRef)}`;
}

/**
 * Validates that a checkout URL returned by the booking API points exclusively
 * to either the local non-production sandbox route (`/checkout/sandbox`) or an
 * official `https://checkout.stripe.com` hosted checkout session without userinfo.
 */
export function isSafeCheckoutRedirectUrl(
  rawUrl: string,
  currentOrigin?: string,
): boolean {
  const target = (rawUrl || "").trim();
  if (!target || target.includes("\\") || /[\r\n\t]/.test(target)) {
    return false;
  }
  if (target.startsWith("/")) {
    if (target.startsWith("//")) return false;
    try {
      const parsed = new URL(target, "https://lovelace.local");
      return parsed.pathname === "/checkout/sandbox";
    } catch {
      return false;
    }
  }
  try {
    const parsed = new URL(target);
    if (parsed.username || parsed.password) return false;
    if (
      parsed.protocol === "https:" &&
      parsed.hostname.toLowerCase() === "checkout.stripe.com"
    ) {
      return true;
    }
    if (currentOrigin) {
      const self = new URL(currentOrigin);
      if (
        parsed.origin.toLowerCase() === self.origin.toLowerCase() &&
        parsed.pathname === "/checkout/sandbox"
      ) {
        return true;
      }
    }
    return false;
  } catch {
    return false;
  }
}

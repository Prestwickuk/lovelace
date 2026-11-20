import "server-only";

import { cache } from "react";
import type {
  ApiErrorEnvelope,
  BookingQuote,
  CreateCheckoutResponse,
  CreateHoldResponse,
  PublicAvailabilityResponse,
  PublicCatalogResponse,
  PublicConfirmationView,
  PublicPropertyBranding,
  PublicUnitTypeDetailResponse,
  QuoteExtraSelection,
} from "./types";

const API_BASE = (
  process.env.LOVELACE_API_BASE_URL || "http://127.0.0.1:8080"
).replace(/\/+$/, "");

const PROPERTY_SLUG = process.env.LOVELACE_PROPERTY_SLUG || "tir-bach";
const PUBLIC_API_SECRET = process.env.LOVELACE_PUBLIC_API_SECRET || "";

export class LovelaceApiError extends Error {
  status: number;
  code: string;
  fields?: Record<string, string>;

  constructor(
    status: number,
    code: string,
    message: string,
    fields?: Record<string, string>,
  ) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

const SAFE_SEGMENT_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
const DEFAULT_UPSTREAM_TIMEOUT_MS = 15_000;

function assertSafePathSegment(value: string, fieldName: string): string {
  const clean = (value || "").trim();
  if (!SAFE_SEGMENT_RE.test(clean)) {
    throw new LovelaceApiError(400, "invalid_param", `Invalid ${fieldName}.`);
  }
  return clean;
}

function resolvePropertySlug(candidate?: unknown): string {
  if (typeof candidate === "string" && candidate.trim() !== "") {
    return assertSafePathSegment(candidate, "property slug");
  }
  return assertSafePathSegment(PROPERTY_SLUG, "property slug");
}

function assertValidUuid(value: string, fieldName: string): string {
  const clean = (value || "").trim();
  if (!UUID_RE.test(clean)) {
    throw new LovelaceApiError(400, "invalid_id", `Invalid ${fieldName}.`);
  }
  return clean;
}

function assertSafeBearerToken(value: string): string {
  const clean = (value || "").trim();
  if (!clean || clean.length > 512 || /[\r\n\s]/.test(clean)) {
    throw new LovelaceApiError(400, "invalid_token", "Invalid confirmation token.");
  }
  return clean;
}

export function isStripeSandboxAllowed(): boolean {
  const envDict: Record<string, string | undefined> = process.env;
  const appEnv = (envDict.LOVELACE_ENV || "").trim().toLowerCase();
  if (appEnv === "production" || appEnv === "staging") {
    return false;
  }
  const flag = (envDict.LOVELACE_STRIPE_SANDBOX || "").trim().toLowerCase();
  if ((envDict.NODE_ENV || "").trim().toLowerCase() === "production") {
    return flag === "true";
  }
  return flag !== "false";
}

export async function callLovelacePublic<T>(
  path: string,
  init: RequestInit = {},
  clientIp = "",
): Promise<T> {
  const headers = new Headers(init.headers);
  if (!headers.has("Content-Type") && init.body) {
    headers.set("Content-Type", "application/json");
  }
  if (PUBLIC_API_SECRET) {
    headers.set("X-Lovelace-Public-Key", PUBLIC_API_SECRET);
  }
  if (clientIp) {
    headers.set("X-Lovelace-Client-IP", clientIp);
  }

  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers,
    cache: "no-store",
    signal: init.signal ?? AbortSignal.timeout(DEFAULT_UPSTREAM_TIMEOUT_MS),
  });

  const text = await res.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }

  if (!res.ok) {
    const errEnv = (parsed || {}) as ApiErrorEnvelope;
    throw new LovelaceApiError(
      res.status,
      errEnv.error?.code || "api_error",
      errEnv.error?.message || `Request failed (${res.status})`,
      errEnv.error?.fields,
    );
  }

  return parsed as T;
}

export function getPropertySlug(): string {
  return PROPERTY_SLUG;
}

export const fetchPublicCatalog = cache(
  async (slug = PROPERTY_SLUG): Promise<PublicCatalogResponse> => {
    const cleanSlug = assertSafePathSegment(slug, "property slug");
    return callLovelacePublic<PublicCatalogResponse>(
      `/api/public/v1/accommodation?property=${encodeURIComponent(cleanSlug)}`,
    );
  },
);

export async function fetchAccommodationDetail(
  code: string,
  slug = PROPERTY_SLUG,
): Promise<PublicUnitTypeDetailResponse> {
  const cleanCode = assertSafePathSegment(code, "accommodation code");
  const cleanSlug = assertSafePathSegment(slug, "property slug");
  return callLovelacePublic<PublicUnitTypeDetailResponse>(
    `/api/public/v1/accommodation/${encodeURIComponent(cleanCode)}?property=${encodeURIComponent(cleanSlug)}`,
  );
}

export async function searchPublicAvailability(
  params: {
    property_slug?: string;
    arrival_date: string;
    departure_date: string;
    adults: number;
    children: number;
    infants: number;
    category?: string;
  },
  clientIp = "",
): Promise<PublicAvailabilityResponse> {
  const propertySlug = resolvePropertySlug(params.property_slug);
  return callLovelacePublic<PublicAvailabilityResponse>(
    "/api/public/v1/availability",
    {
      method: "POST",
      body: JSON.stringify({
        property_slug: propertySlug,
        arrival_date: params.arrival_date,
        departure_date: params.departure_date,
        adults: params.adults,
        children: params.children,
        infants: params.infants,
        category: params.category || "",
      }),
    },
    clientIp,
  );
}

export async function computePublicPricing(
  params: {
    property_slug?: string;
    unit_type_code: string;
    arrival_date: string;
    departure_date: string;
    adults: number;
    children: number;
    infants: number;
    extras: QuoteExtraSelection[];
  },
  clientIp = "",
): Promise<{ quote: BookingQuote }> {
  const propertySlug = resolvePropertySlug(params.property_slug);
  return callLovelacePublic<{ quote: BookingQuote }>(
    "/api/public/v1/pricing",
    {
      method: "POST",
      body: JSON.stringify({
        property_slug: propertySlug,
        unit_type_code: params.unit_type_code,
        arrival_date: params.arrival_date,
        departure_date: params.departure_date,
        adults: params.adults,
        children: params.children,
        infants: params.infants,
        extras: params.extras,
      }),
    },
    clientIp,
  );
}

export async function createPublicHold(
  payload: Record<string, unknown>,
  clientIp = "",
): Promise<CreateHoldResponse> {
  const propertySlug = resolvePropertySlug(payload.property_slug);
  return callLovelacePublic<CreateHoldResponse>(
    "/api/public/v1/booking/holds",
    {
      method: "POST",
      body: JSON.stringify({
        ...payload,
        property_slug: propertySlug,
      }),
    },
    clientIp,
  );
}

export async function cancelPublicHold(
  holdId: string,
  clientIp = "",
): Promise<unknown> {
  const cleanHoldId = assertValidUuid(holdId, "hold ID");
  return callLovelacePublic(
    `/api/public/v1/booking/holds/${encodeURIComponent(cleanHoldId)}/cancel`,
    { method: "POST", body: JSON.stringify({}) },
    clientIp,
  );
}

export async function initiatePublicCheckout(
  payload: {
    hold_id: string;
    idempotency_key?: string;
  },
  clientIp = "",
): Promise<CreateCheckoutResponse> {
  const cleanHoldId = assertValidUuid(payload.hold_id, "hold ID");
  return callLovelacePublic<CreateCheckoutResponse>(
    "/api/public/v1/checkout",
    {
      method: "POST",
      body: JSON.stringify({
        hold_id: cleanHoldId,
        idempotency_key: payload.idempotency_key,
      }),
    },
    clientIp,
  );
}

export async function fetchBookingConfirmation(
  ref: string,
  token: string,
  clientIp = "",
): Promise<PublicConfirmationView> {
  const cleanToken = assertSafeBearerToken(token);
  return callLovelacePublic<PublicConfirmationView>(
    "/api/public/v1/booking-confirmation/lookup",
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${cleanToken}`,
      },
      body: JSON.stringify({
        booking_reference: ref,
      }),
    },
    clientIp,
  );
}

export async function rotateBookingConfirmationToken(
  ref: string,
  token: string,
  clientIp = "",
): Promise<{ confirmation_token: string }> {
  const cleanToken = assertSafeBearerToken(token);
  return callLovelacePublic<{ confirmation_token: string }>(
    "/api/public/v1/booking-confirmation/rotate-token",
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${cleanToken}`,
      },
      body: JSON.stringify({
        booking_reference: ref,
      }),
    },
    clientIp,
  );
}

export async function requestBookingCancellation(
  payload: {
    booking_reference: string;
    token: string;
    note: string;
  },
  clientIp = "",
): Promise<PublicConfirmationView> {
  const cleanToken = assertSafeBearerToken(payload.token);
  return callLovelacePublic<PublicConfirmationView>(
    "/api/public/v1/booking-confirmation/cancel-request",
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${cleanToken}`,
      },
      body: JSON.stringify({
        booking_reference: payload.booking_reference,
        note: payload.note,
      }),
    },
    clientIp,
  );
}

export async function fetchPublicPropertyBranding(
  slug = PROPERTY_SLUG,
): Promise<PublicPropertyBranding | undefined> {
  try {
    const catalog = await fetchPublicCatalog(slug);
    return catalog.property.branding;
  } catch {
    return undefined;
  }
}

export async function fetchPublicPropertyLogo(
  slug = PROPERTY_SLUG,
  clientIp = "",
): Promise<Response> {
  const cleanSlug = assertSafePathSegment(slug, "property slug");
  const headers = new Headers();
  if (PUBLIC_API_SECRET) {
    headers.set("X-Lovelace-Public-Key", PUBLIC_API_SECRET);
  }
  if (clientIp) {
    headers.set("X-Lovelace-Client-IP", clientIp);
  }
  return fetch(`${API_BASE}/api/public/v1/properties/${encodeURIComponent(cleanSlug)}/logo`, {
    method: "GET",
    headers,
    cache: "no-store",
    signal: AbortSignal.timeout(DEFAULT_UPSTREAM_TIMEOUT_MS),
  });
}

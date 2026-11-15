import "server-only";

import type {
  ApiErrorEnvelope,
  BookingQuote,
  CreateCheckoutResponse,
  CreateHoldResponse,
  PublicAvailabilityResponse,
  PublicCatalogResponse,
  PublicConfirmationView,
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

export async function fetchPublicCatalog(
  slug = PROPERTY_SLUG,
): Promise<PublicCatalogResponse> {
  return callLovelacePublic<PublicCatalogResponse>(
    `/api/public/v1/accommodation?property=${encodeURIComponent(slug)}`,
  );
}

export async function fetchAccommodationDetail(
  code: string,
  slug = PROPERTY_SLUG,
): Promise<PublicUnitTypeDetailResponse> {
  return callLovelacePublic<PublicUnitTypeDetailResponse>(
    `/api/public/v1/accommodation/${encodeURIComponent(code)}?property=${encodeURIComponent(slug)}`,
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
  return callLovelacePublic<PublicAvailabilityResponse>(
    "/api/public/v1/availability",
    {
      method: "POST",
      body: JSON.stringify({
        property_slug: params.property_slug || PROPERTY_SLUG,
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
  return callLovelacePublic<{ quote: BookingQuote }>(
    "/api/public/v1/pricing",
    {
      method: "POST",
      body: JSON.stringify({
        property_slug: params.property_slug || PROPERTY_SLUG,
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
  return callLovelacePublic<CreateHoldResponse>(
    "/api/public/v1/booking/holds",
    {
      method: "POST",
      body: JSON.stringify({
        property_slug: PROPERTY_SLUG,
        ...payload,
      }),
    },
    clientIp,
  );
}

export async function cancelPublicHold(
  holdId: string,
  clientIp = "",
): Promise<unknown> {
  return callLovelacePublic(
    `/api/public/v1/booking/holds/${encodeURIComponent(holdId)}/cancel`,
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
  return callLovelacePublic<CreateCheckoutResponse>(
    "/api/public/v1/checkout",
    {
      method: "POST",
      body: JSON.stringify({
        hold_id: payload.hold_id,
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
  return callLovelacePublic<PublicConfirmationView>(
    "/api/public/v1/booking-confirmation/lookup",
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
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
  return callLovelacePublic<{ confirmation_token: string }>(
    "/api/public/v1/booking-confirmation/rotate-token",
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
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
  return callLovelacePublic<PublicConfirmationView>(
    "/api/public/v1/booking-confirmation/cancel-request",
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${payload.token}`,
      },
      body: JSON.stringify({
        booking_reference: payload.booking_reference,
        note: payload.note,
      }),
    },
    clientIp,
  );
}

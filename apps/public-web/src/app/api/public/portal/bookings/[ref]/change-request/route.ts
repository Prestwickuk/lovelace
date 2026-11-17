import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSafeBookingRef,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalEnquiryView } from "@/lib/types";

export async function POST(
  req: NextRequest,
  context: { params: Promise<{ ref: string }> },
) {
  const limited = enforcePortalBffRateLimit(req, "portal-booking-change", 15);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const { ref } = await context.params;
    const cleanRef = assertSafeBookingRef(ref);
    const body = await parseJsonObject<{
      requested_arrival?: string;
      requested_departure?: string;
      requested_adults?: number;
      requested_children?: number;
      requested_infants?: number;
      notes?: string;
    }>(req);
    const res = await callLovelacePortal<{ enquiry: PortalEnquiryView }>(
      req,
      `/bookings/${encodeURIComponent(cleanRef)}/change-request`,
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          requested_arrival: body.requested_arrival || "",
          requested_departure: body.requested_departure || "",
          requested_adults: Number(body.requested_adults || 0),
          requested_children: Number(body.requested_children || 0),
          requested_infants: Number(body.requested_infants || 0),
          notes: body.notes || "",
        }),
      },
      { requireSession: true, requireCsrf: true },
    );
    return NextResponse.json(res, {
      status: 201,
      headers: PRIVATE_NO_STORE_HEADERS,
    });
  } catch (err) {
    return handlePortalBffError(err);
  }
}

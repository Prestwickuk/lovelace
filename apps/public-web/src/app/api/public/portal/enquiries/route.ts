import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalEnquiryView } from "@/lib/types";

export async function GET(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-enquiries-list", 60);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    const res = await callLovelacePortal<{ enquiries: PortalEnquiryView[] }>(
      req,
      "/enquiries",
      { method: "GET" },
      { requireSession: true, requireCsrf: false },
    );
    return NextResponse.json(res, {
      status: 200,
      headers: PRIVATE_NO_STORE_HEADERS,
    });
  } catch (err) {
    return handlePortalBffError(err);
  }
}

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-enquiries-create", 15);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = (await req.json()) as {
      category?: string;
      booking_reference?: string;
      subject?: string;
      message?: string;
    };
    const res = await callLovelacePortal<{ enquiry: PortalEnquiryView }>(
      req,
      "/enquiries",
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          category: body.category || "GENERAL",
          booking_reference: body.booking_reference || "",
          subject: body.subject || "",
          message: body.message || "",
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

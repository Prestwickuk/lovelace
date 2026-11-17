import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSafeUuid,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalEnquiryView } from "@/lib/types";

export async function POST(
  req: NextRequest,
  context: { params: Promise<{ id: string }> },
) {
  const limited = enforcePortalBffRateLimit(req, "portal-enquiry-reply", 20);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const { id } = await context.params;
    const cleanId = assertSafeUuid(id, "enquiry ID");
    const body = (await req.json()) as { message?: string };
    const res = await callLovelacePortal<{ enquiry: PortalEnquiryView }>(
      req,
      `/enquiries/${encodeURIComponent(cleanId)}/messages`,
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
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

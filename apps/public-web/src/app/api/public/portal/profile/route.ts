import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalGuestPreferenceView, PortalProfileView } from "@/lib/types";

export async function GET(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-profile-read", 60);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    const res = await callLovelacePortal<{ profile: PortalProfileView }>(
      req,
      "/profile",
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

export async function PATCH(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-profile-update", 20);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = await parseJsonObject<{
      phone?: string;
      preferred_contact_method?: string;
      preferred_language?: string;
      preferences?: PortalGuestPreferenceView[];
    }>(req);
    const payload: Record<string, unknown> = {
      property_slug: getPropertySlug(),
      phone: body.phone || "",
      preferred_contact_method: body.preferred_contact_method || "EMAIL",
      preferred_language: body.preferred_language || "en",
    };
    if (Array.isArray(body.preferences)) {
      payload.preferences = body.preferences;
    }
    const res = await callLovelacePortal<{ profile: PortalProfileView }>(
      req,
      "/profile",
      {
        method: "PATCH",
        body: JSON.stringify(payload),
      },
      { requireSession: true, requireCsrf: true },
    );
    return NextResponse.json(res, {
      status: 200,
      headers: PRIVATE_NO_STORE_HEADERS,
    });
  } catch (err) {
    return handlePortalBffError(err);
  }
}

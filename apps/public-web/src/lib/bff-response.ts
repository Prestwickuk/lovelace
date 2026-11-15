import "server-only";

import { NextResponse } from "next/server";
import { LovelaceApiError } from "./lovelace-api";

export function handleBffError(err: unknown) {
  if (err instanceof LovelaceApiError) {
    return NextResponse.json(
      {
        error: {
          code: err.code,
          message: err.message,
          fields: err.fields,
        },
      },
      { status: err.status },
    );
  }
  return NextResponse.json(
    {
      error: {
        code: "upstream_unavailable",
        message: "Booking service temporarily unavailable. Please try again.",
      },
    },
    { status: 502 },
  );
}

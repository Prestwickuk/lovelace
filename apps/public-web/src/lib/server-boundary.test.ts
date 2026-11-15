import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

function listFiles(dir: string): string[] {
  const results: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results.push(...listFiles(full));
    } else if (/\.(ts|tsx|js|jsx|mjs)$/.test(entry.name)) {
      results.push(full);
    }
  }
  return results;
}

describe("Public frontend server-only credential boundaries", () => {
  const rootDir = path.resolve(__dirname, "../..");

  it("marks lovelace-api and bff-response with explicit import 'server-only'", () => {
    const apiSource = fs.readFileSync(
      path.join(rootDir, "src/lib/lovelace-api.ts"),
      "utf8",
    );
    const bffSource = fs.readFileSync(
      path.join(rootDir, "src/lib/bff-response.ts"),
      "utf8",
    );
    expect(apiSource.startsWith('import "server-only";')).toBe(true);
    expect(bffSource.startsWith('import "server-only";')).toBe(true);
  });

  it("never imports server-only helpers or references LOVELACE_PUBLIC_API_SECRET in client components", () => {
    const srcFiles = listFiles(path.join(rootDir, "src"));
    for (const file of srcFiles) {
      const text = fs.readFileSync(file, "utf8");
      const trimmed = text.trimStart();
      if (
        trimmed.startsWith('"use client";') ||
        trimmed.startsWith("'use client';")
      ) {
        expect(text).not.toContain("lovelace-api");
        expect(text).not.toContain("bff-response");
        expect(text).not.toContain("LOVELACE_PUBLIC_API_SECRET");
      }
    }
  });

  it("never exposes LOVELACE_PUBLIC_API_SECRET via next.config.ts", () => {
    const nextConfig = fs.readFileSync(
      path.join(rootDir, "next.config.ts"),
      "utf8",
    );
    expect(nextConfig).not.toContain("LOVELACE_PUBLIC_API_SECRET");
    expect(nextConfig).not.toContain("NEXT_PUBLIC_LOVELACE_PUBLIC_API_SECRET");
  });
});

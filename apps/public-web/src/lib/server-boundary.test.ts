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

  it("marks lovelace-api, bff-response, portal-bff, and portal-server with explicit import 'server-only'", () => {
    for (const rel of [
      "src/lib/lovelace-api.ts",
      "src/lib/bff-response.ts",
      "src/lib/portal-bff.ts",
      "src/lib/portal-server.ts",
    ]) {
      const src = fs.readFileSync(path.join(rootDir, rel), "utf8");
      expect(src.startsWith('import "server-only";')).toBe(true);
    }
  });

  it("never imports server-only helpers, /api/v1/ admin routes, or LOVELACE_PUBLIC_API_SECRET in client components", () => {
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
        expect(text).not.toContain("portal-bff");
        expect(text).not.toContain("portal-server");
        expect(text).not.toContain("LOVELACE_PUBLIC_API_SECRET");
        expect(text).not.toContain("/api/v1/");
      }
    }
  });

  it("never stores Guest Portal credentials in localStorage or sessionStorage", () => {
    const portalFiles = [
      ...listFiles(path.join(rootDir, "src/components/portal")),
      ...listFiles(path.join(rootDir, "src/app/portal")),
      path.join(rootDir, "src/lib/portal-client.ts"),
    ];
    for (const file of portalFiles) {
      const text = fs.readFileSync(file, "utf8");
      expect(text).not.toContain("localStorage");
      expect(text).not.toContain("sessionStorage");
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

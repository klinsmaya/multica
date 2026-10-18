// @vitest-environment node
import { describe, expect, it } from "vitest";
import { PREFLIGHT_BLOCK_REASONS } from "@multica/core/types";
import enMarket from "../locales/en/market.json";

// The other locales follow en through locales/parity.test.ts.
describe("market block reason copy", () => {
  it("has exactly one en string per server reason plus unknown", () => {
    expect(Object.keys(enMarket.reasons).sort()).toEqual([...PREFLIGHT_BLOCK_REASONS, "unknown"].sort());
  });
});

"use client";

import type { ComponentType } from "react";
import { useFeatureEnabled } from "@multica/core/config";
import { MARKET_V1_FLAG } from "@multica/core/feature-flags";

/** Renders nothing while market_v1 is off, so a gated page cannot leak its content or requests. */
export function withMarketFlag<P extends object>(Page: ComponentType<P>): ComponentType<P> {
  function Gated(props: P) {
    const enabled = useFeatureEnabled(MARKET_V1_FLAG, false);
    return enabled ? <Page {...props} /> : null;
  }
  Gated.displayName = `withMarketFlag(${Page.displayName ?? Page.name})`;
  return Gated;
}

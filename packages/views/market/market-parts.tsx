"use client";

import type { ReactNode } from "react";
import { ApiError } from "@multica/core/api";
import type { MarketSource } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n";

export function SourceBadge({ source }: { source: MarketSource }) {
  const { t } = useT("market");
  if (source === "live") return null;
  return (
    <Badge variant="outline">
      {source === "stub" ? t(($) => $.source.stub) : t(($) => $.source.unknown)}
    </Badge>
  );
}

export function MarketMessage({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 py-16 text-center">
      <p className="text-body text-muted-foreground">{children}</p>
      {action}
    </div>
  );
}

/** A 404 is a missing listing; anything else is the catalog failing, which a retry may fix. */
export function MarketError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const { t } = useT("market");
  if (error instanceof ApiError && error.status === 404) {
    return <MarketMessage>{t(($) => $.errors.not_found)}</MarketMessage>;
  }
  return (
    <MarketMessage
      action={
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>
          {t(($) => $.errors.retry)}
        </Button>
      }
    >
      {t(($) => $.errors.unavailable)}
    </MarketMessage>
  );
}

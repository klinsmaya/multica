"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import {
  buildPreflightRequest,
  preflightMarket,
  workspacePreflightIdentity,
  type MarketResource,
  type PreflightResult,
} from "@multica/core/market";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n";

export function InstallWizard({
  workspaceId,
  resource,
}: {
  workspaceId: string;
  resource: MarketResource;
}) {
  const { t } = useT("market");
  const userId = useAuthStore((state) => state.user?.id ?? "");
  const [result, setResult] = useState<PreflightResult | null>(null);
  const [failed, setFailed] = useState(false);

  const check = useMutation({
    mutationFn: () =>
      preflightMarket(
        workspaceId,
        buildPreflightRequest({
          identity: workspacePreflightIdentity({ workspaceId, userId }),
          demands: resource.demands,
        }),
      ),
    onSuccess: (next) => {
      setFailed(false);
      setResult(next);
    },
    onError: () => {
      setResult(null);
      setFailed(true);
    },
  });

  const ready = result?.status === "ready" && result.denials.length === 0;

  return (
    <section className="flex flex-col gap-4">
      <ul className="flex flex-col gap-1">
        {resource.demands.map((demand) => (
          <li key={`${demand.kind}:${demand.ref}:${demand.scope}`} className="text-body">
            <span className="text-muted-foreground">{demand.kind}</span> {demand.ref}{" "}
            <span className="text-caption text-muted-foreground">{demand.scope}</span>
          </li>
        ))}
      </ul>
      <div>
        <Button
          type="button"
          disabled={check.isPending}
          aria-busy={check.isPending}
          onClick={() => check.mutate()}
        >
          {t(($) => $.check_plan)}
        </Button>
      </div>
      {failed ? <p className="text-body">{t(($) => $.check_failed)}</p> : null}
      {result && ready ? (
        <p className="text-body">{t(($) => $.ready)}</p>
      ) : null}
      {result && !ready && result.denials.length === 0 ? (
        <p className="text-body">{t(($) => $.unreadable)}</p>
      ) : null}
      {result && result.denials.length > 0 ? (
        <ul className="flex flex-col gap-1">
          {result.denials.map((denial) => (
            <li key={`${denial.kind}:${denial.ref}:${denial.code}`} className="text-body">
              <span>{denial.kind}</span> <span>{denial.ref}</span>{" "}
              <span>{denial.code}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

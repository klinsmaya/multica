"use client";

import { useMemo } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import type { MarketPreflightInput } from "@multica/core/types";
import { useNavigation } from "../navigation";

export function useInstallParams(listingId: string): {
  input: MarketPreflightInput | null;
  chooseRuntime: (runtimeId: string) => void;
  chooseModel: (model: string) => void;
} {
  const navigation = useNavigation();
  const p = useWorkspacePaths();
  const version = navigation.searchParams.get("version") ?? "";
  const runtime = navigation.searchParams.get("runtime") ?? "";
  const model = navigation.searchParams.get("model") ?? "";

  const input = useMemo<MarketPreflightInput | null>(
    () =>
      version
        ? { listingId, version, runtimeId: runtime || undefined, model: model || undefined }
        : null,
    [listingId, version, runtime, model],
  );

  return {
    input,
    chooseRuntime: (runtimeId) => navigation.replace(p.marketInstall(listingId, version, runtimeId)),
    chooseModel: (next) =>
      navigation.replace(p.marketInstall(listingId, version, runtime || undefined, next || undefined)),
  };
}

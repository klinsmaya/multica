"use client";

import type { ReactNode } from "react";
import { useIsFetching, useQueryClient } from "@tanstack/react-query";
import { useWorkspacePaths } from "@multica/core/paths";
import { runtimeModelsKeys, runtimeModelsOptions } from "@multica/core/runtimes";
import type {
  ModelCheck,
  OpenPreflight,
  PreflightBlockReason,
  PreflightCheck,
  ReadyPreflight,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { cn } from "@multica/ui/lib/utils";
import { Circle, CircleCheck, CircleX } from "lucide-react";
import { useT } from "../i18n";
import { AppLink } from "../navigation";

export function PreflightChecklist({
  report,
  onChooseRuntime,
  onChooseModel,
  onRecheck,
  rechecking,
}: {
  report: ReadyPreflight | OpenPreflight;
  onChooseRuntime: (runtimeId: string) => void;
  onChooseModel: (model: string) => void;
  onRecheck: () => void;
  rechecking: boolean;
}) {
  const { t } = useT("market");
  let summary: string;
  if (report.status === "ready") summary = t(($) => $.install.ready);
  else if (report.source === "stub") summary = t(($) => $.install.stub);
  else if (report.status === "choose") summary = t(($) => $.install.choose);
  else summary = t(($) => $.install.blocked);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p role="status" className="text-body-lg font-medium">
          {summary}
        </p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onRecheck}
          disabled={rechecking}
          aria-busy={rechecking}
        >
          {t(($) => $.install.recheck)}
        </Button>
      </div>
      <ModelSection
        check={report.model}
        onChooseRuntime={onChooseRuntime}
        onChooseModel={onChooseModel}
        onRecheck={onRecheck}
      />
      <CheckSection title={t(($) => $.detail.tools)} checks={report.tools} />
      <CheckSection title={t(($) => $.detail.connections)} checks={report.connections} />
      <CheckSection title={t(($) => $.detail.knowledge)} checks={report.knowledge} />
      <CheckSection title={t(($) => $.detail.unsupported)} checks={report.unsupported} />
    </div>
  );
}

function ModelSection({
  check,
  onChooseRuntime,
  onChooseModel,
  onRecheck,
}: {
  check: ModelCheck;
  onChooseRuntime: (runtimeId: string) => void;
  onChooseModel: (model: string) => void;
  onRecheck: () => void;
}) {
  const { t } = useT("market");
  const selectedId = check.status === "satisfied" ? check.runtime_id : null;
  const models = check.options.find((option) => option.runtime_id === selectedId)?.models ?? [];

  return (
    <section className="space-y-3">
      <div className="flex items-start gap-2">
        <StatusIcon status={check.status} />
        <div className="min-w-0">
          <h2 className="text-body font-medium">
            {t(($) => $.detail.model)} <span className="font-normal text-muted-foreground">{check.provider}</span>
          </h2>
          {check.status === "blocked" ? <Reason reason={check.reason} /> : null}
        </div>
      </div>
      {check.options.length > 0 ? (
        <fieldset className="space-y-1 pl-6">
          <legend className="mb-1 text-caption text-muted-foreground">{t(($) => $.install.runtime)}</legend>
          {check.options.map((option) => (
            <div key={option.runtime_id} className="flex items-center gap-3">
              <label
                className={cn(
                  "flex min-w-0 flex-1 items-start gap-2 rounded-md px-2 py-1.5",
                  option.usable ? "cursor-pointer hover:bg-surface-hover" : "text-muted-foreground",
                )}
              >
                <input
                  type="radio"
                  name="market-runtime"
                  value={option.runtime_id}
                  checked={option.runtime_id === selectedId}
                  disabled={!option.usable}
                  onChange={() => onChooseRuntime(option.runtime_id)}
                  className="mt-1 size-4 shrink-0 accent-foreground"
                />
                <span className="min-w-0">
                  <span className="block truncate text-body">{option.label}</span>
                  {option.reason ? <Reason reason={option.reason} /> : null}
                </span>
              </label>
              {option.reason === "model_catalog_unknown" ? (
                <LoadModelsButton runtimeId={option.runtime_id} onLoaded={onRecheck} />
              ) : null}
            </div>
          ))}
        </fieldset>
      ) : null}
      {check.status === "satisfied" && models.length > 0 ? (
        <div className="pl-6">
          <Select
            items={models.map((model) => ({ value: model, label: model }))}
            value={check.model ?? ""}
            onValueChange={(next) => next && onChooseModel(next)}
          >
            <SelectTrigger size="sm" className="w-72 max-w-full" aria-label={t(($) => $.install.model)}>
              <SelectValue placeholder={t(($) => $.install.default_model)} />
            </SelectTrigger>
            <SelectContent align="start">
              {models.map((model) => (
                <SelectItem key={model} value={model}>
                  {model}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      ) : null}
    </section>
  );
}

function LoadModelsButton({ runtimeId, onLoaded }: { runtimeId: string; onLoaded: () => void }) {
  const { t } = useT("market");
  const queryClient = useQueryClient();
  const loading = useIsFetching({ queryKey: runtimeModelsKeys.forRuntime(runtimeId) }) > 0;
  const load = async () => {
    try {
      await queryClient.fetchQuery(runtimeModelsOptions(runtimeId));
    } catch {
    } finally {
      onLoaded();
    }
  };
  return (
    <Button type="button" variant="outline" size="sm" onClick={() => void load()} disabled={loading} aria-busy={loading}>
      {t(($) => $.install.load_models)}
    </Button>
  );
}

function CheckSection({ title, checks }: { title: string; checks: PreflightCheck[] }) {
  if (checks.length === 0) return null;
  return (
    <section className="space-y-2">
      <h2 className="text-body font-medium">{title}</h2>
      <ul className="space-y-2">
        {checks.map((check, i) => (
          <CheckRow key={`${check.name}-${i}`} check={check} />
        ))}
      </ul>
    </section>
  );
}

function CheckRow({ check }: { check: PreflightCheck }) {
  const { t } = useT("market");
  const p = useWorkspacePaths();
  let detail: ReactNode;
  if (check.status === "satisfied") {
    detail = (
      <p className="text-caption text-muted-foreground">
        {check.binding.action === "use_existing" ? t(($) => $.install.use_existing) : t(($) => $.install.provision)}
      </p>
    );
  } else {
    detail = <Reason reason={check.reason} />;
  }
  return (
    <li className="flex items-start gap-2">
      <StatusIcon status={check.status} />
      <div className="min-w-0 flex-1">
        <p className="break-words text-body">{check.name}</p>
        {detail}
      </div>
      {check.status === "blocked" && check.reason === "connection_not_connected" ? (
        <Button
          size="sm"
          variant="outline"
          render={<AppLink href={`${p.settings()}?tab=apps`} />}
          nativeButton={false}
        >
          {t(($) => $.install.connect)}
        </Button>
      ) : null}
    </li>
  );
}

function Reason({ reason }: { reason: PreflightBlockReason }) {
  const { t } = useT("market");
  return <p className="text-caption text-muted-foreground">{t(($) => $.reasons[reason])}</p>;
}

function StatusIcon({ status }: { status: "satisfied" | "blocked" | "choose" }) {
  const { t } = useT("market");
  const label = t(($) => $.install.status[status]);
  const className = "mt-0.5 size-4 shrink-0";
  if (status === "satisfied") return <CircleCheck role="img" aria-label={label} className={cn(className, "text-success")} />;
  if (status === "blocked") return <CircleX role="img" aria-label={label} className={cn(className, "text-destructive")} />;
  return <Circle role="img" aria-label={label} className={cn(className, "text-muted-foreground")} />;
}

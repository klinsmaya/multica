"use client";

import { useQuery } from "@tanstack/react-query";
import { adminOverviewOptions } from "@multica/core/admin";
import { useT } from "../i18n";
import {
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
} from "./components/shared";

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-lg border p-4">
      <p className="text-caption text-muted-foreground">{label}</p>
      <p className="mt-1 text-display-sm font-semibold tabular-nums">{value}</p>
    </div>
  );
}

export function AdminOverviewPage() {
  const { t } = useT("admin");
  const overview = useQuery(adminOverviewOptions());

  return (
    <>
      <AdminPageHeader title={t(($) => $.overview.title)} />
      <AdminPageBody>
        {overview.isPending ? (
          <AdminLoading />
        ) : overview.isError ? (
          <AdminError onRetry={() => void overview.refetch()} />
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
              <Stat
                label={t(($) => $.overview.users_total)}
                value={overview.data.users_total}
              />
              <Stat
                label={t(($) => $.overview.users_new_7d)}
                value={overview.data.users_new_7d}
              />
              <Stat
                label={t(($) => $.overview.users_disabled)}
                value={overview.data.users_disabled}
              />
              <Stat
                label={t(($) => $.overview.workspaces_total)}
                value={overview.data.workspaces_total}
              />
              <Stat
                label={t(($) => $.overview.runtimes_total)}
                value={overview.data.runtimes_total}
              />
              <Stat
                label={t(($) => $.overview.runtimes_online)}
                value={overview.data.runtimes_online}
              />
            </div>
            <section className="space-y-2">
              <h2 className="text-body font-medium">
                {t(($) => $.overview.tasks_24h)}
              </h2>
              {Object.keys(overview.data.tasks_24h).length === 0 ? (
                <p className="text-caption text-muted-foreground">
                  {t(($) => $.overview.tasks_none)}
                </p>
              ) : (
                <dl className="grid grid-cols-2 gap-3 md:grid-cols-4">
                  {Object.entries(overview.data.tasks_24h)
                    .sort(([a], [b]) => a.localeCompare(b))
                    .map(([status, count]) => (
                      <div key={status} className="rounded-lg border p-3">
                        <dt className="font-mono text-caption text-muted-foreground">
                          {status}
                        </dt>
                        <dd className="mt-1 text-body font-medium tabular-nums">
                          {count}
                        </dd>
                      </div>
                    ))}
                </dl>
              )}
            </section>
          </>
        )}
      </AdminPageBody>
    </>
  );
}

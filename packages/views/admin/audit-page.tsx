"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { adminAuditOptions } from "@multica/core/admin";
import { paths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import {
  ADMIN_PAGE_SIZE,
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  AdminPagination,
  useAdminDate,
} from "./components/shared";

function targetHref(type: string, id: string): string | null {
  if (!id) return null;
  if (type === "user") return paths.admin.user(id);
  if (type === "workspace") return paths.admin.workspace(id);
  return null;
}

export function AdminAuditPage() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const [draft, setDraft] = useState({ action: "", target: "" });
  const [filter, setFilter] = useState({ action: "", target: "" });
  const [page, setPage] = useState(1);

  const audit = useQuery(
    adminAuditOptions({
      action: filter.action || undefined,
      target_id: filter.target || undefined,
      page,
      page_size: ADMIN_PAGE_SIZE,
    }),
  );

  return (
    <>
      <AdminPageHeader title={t(($) => $.audit.title)} count={audit.data?.total} />
      <AdminPageBody>
        <form
          className="flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setFilter({ action: draft.action.trim(), target: draft.target.trim() });
          }}
        >
          <Input
            value={draft.action}
            onChange={(e) => setDraft((d) => ({ ...d, action: e.target.value }))}
            placeholder={t(($) => $.audit.filter_action)}
            className="w-56 font-mono"
          />
          <Input
            value={draft.target}
            onChange={(e) => setDraft((d) => ({ ...d, target: e.target.value }))}
            placeholder={t(($) => $.audit.filter_target)}
            className="w-80 font-mono"
          />
          <Button type="submit" variant="outline" size="sm">
            {t(($) => $.audit.apply)}
          </Button>
          {filter.action || filter.target ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setDraft({ action: "", target: "" });
                setFilter({ action: "", target: "" });
                setPage(1);
              }}
            >
              {t(($) => $.audit.clear)}
            </Button>
          ) : null}
        </form>

        {audit.isPending ? (
          <AdminLoading />
        ) : audit.isError ? (
          <AdminError onRetry={() => void audit.refetch()} />
        ) : audit.data.items.length === 0 ? (
          <p className="py-8 text-center text-caption text-muted-foreground">
            {t(($) => $.audit.empty)}
          </p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.audit.col_when)}</TableHead>
                  <TableHead>{t(($) => $.audit.col_actor)}</TableHead>
                  <TableHead>{t(($) => $.audit.col_action)}</TableHead>
                  <TableHead>{t(($) => $.audit.col_target)}</TableHead>
                  <TableHead>{t(($) => $.audit.col_reason)}</TableHead>
                  <TableHead>{t(($) => $.audit.col_ip)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {audit.data.items.map((entry) => {
                  const href = targetHref(entry.target_type, entry.target_id);
                  const target = `${entry.target_type} ${entry.target_id}`;
                  return (
                    <TableRow key={entry.id}>
                      <TableCell className="whitespace-nowrap text-muted-foreground">
                        {formatDate(entry.created_at)}
                      </TableCell>
                      <TableCell>
                        <AppLink
                          href={paths.admin.user(entry.actor_user_id)}
                          className="hover:underline"
                        >
                          {entry.actor_email || entry.actor_user_id}
                        </AppLink>
                      </TableCell>
                      <TableCell className="font-mono text-caption">
                        {entry.action}
                      </TableCell>
                      <TableCell className="font-mono text-caption">
                        {href ? (
                          <AppLink href={href} className="hover:underline">
                            {target}
                          </AppLink>
                        ) : (
                          target
                        )}
                      </TableCell>
                      <TableCell className="max-w-md whitespace-pre-wrap">
                        {entry.reason}
                      </TableCell>
                      <TableCell className="font-mono text-caption text-muted-foreground">
                        {entry.ip}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
            <AdminPagination page={audit.data} onPageChange={setPage} />
          </>
        )}
      </AdminPageBody>
    </>
  );
}

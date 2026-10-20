"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { adminWorkspaceOptions } from "@multica/core/admin";
import { paths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
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
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  useAdminDate,
} from "./components/shared";

export function AdminWorkspaceDetailPage({ workspaceId }: { workspaceId: string }) {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const workspace = useQuery(adminWorkspaceOptions(workspaceId));

  const back = (
    <Button
      variant="ghost"
      size="icon-sm"
      render={<AppLink href={paths.admin.workspaces()} />}
      aria-label={t(($) => $.nav.workspaces)}
    >
      <ArrowLeft aria-hidden="true" />
    </Button>
  );

  if (workspace.isPending) {
    return (
      <>
        <AdminPageHeader title={t(($) => $.workspaces.detail_title)} actions={back} />
        <AdminLoading />
      </>
    );
  }
  if (workspace.isError) {
    return (
      <>
        <AdminPageHeader title={t(($) => $.workspaces.detail_title)} actions={back} />
        <AdminError onRetry={() => void workspace.refetch()} />
      </>
    );
  }

  const w = workspace.data;

  return (
    <>
      <AdminPageHeader
        title={w.name}
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              render={<AppLink href={paths.workspace(w.slug).issues()} target="_blank" />}
            >
              <ExternalLink className="size-4" aria-hidden="true" />
              {t(($) => $.workspaces.open_in_app)}
            </Button>
            {back}
          </>
        }
      />
      <AdminPageBody>
        <section className="flex flex-wrap items-center gap-x-4 gap-y-1 text-caption text-muted-foreground">
          <span className="font-mono">{w.slug}</span>
          <span>{formatDate(w.created_at)}</span>
          <span className="tabular-nums">
            {t(($) => $.workspaces.col_members)}: {w.member_count}
          </span>
          <span className="tabular-nums">
            {t(($) => $.workspaces.col_agents)}: {w.agent_count}
          </span>
        </section>

        <section className="space-y-2">
          <h2 className="text-body font-medium">
            {t(($) => $.workspaces.members_title)}
          </h2>
          {w.members.length === 0 ? (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.workspaces.members_empty)}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.workspaces.col_member)}</TableHead>
                  <TableHead>{t(($) => $.workspaces.col_role)}</TableHead>
                  <TableHead>{t(($) => $.workspaces.col_joined)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {w.members.map((m) => (
                  <TableRow key={m.user_id}>
                    <TableCell>
                      <AppLink
                        href={paths.admin.user(m.user_id)}
                        className="block min-w-0 hover:underline"
                      >
                        <span className="font-medium">{m.name || m.email}</span>
                        <span className="ml-2 text-caption text-muted-foreground">
                          {m.email}
                        </span>
                      </AppLink>
                      {m.disabled_at ? (
                        <Badge variant="destructive" className="ml-2">
                          {t(($) => $.workspaces.member_disabled)}
                        </Badge>
                      ) : null}
                    </TableCell>
                    <TableCell>{m.role}</TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(m.joined_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </section>
      </AdminPageBody>
    </>
  );
}

"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { toast } from "sonner";
import {
  adminUserOptions,
  useAdminDisableUser,
  useAdminEnableUser,
  useAdminRevokeUserToken,
  type AdminUserToken,
} from "@multica/core/admin";
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
import { canOperate, useAdminRole } from "./admin-shell";
import {
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  ReasonDialog,
  useAdminDate,
} from "./components/shared";

export function AdminUserDetailPage({ userId }: { userId: string }) {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const role = useAdminRole();
  const user = useQuery(adminUserOptions(userId));
  const disable = useAdminDisableUser();
  const enable = useAdminEnableUser();
  const revoke = useAdminRevokeUserToken();
  const [statusDialog, setStatusDialog] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<AdminUserToken | null>(null);

  const back = (
    <Button
      variant="ghost"
      size="icon-sm"
      render={<AppLink href={paths.admin.users()} />}
      aria-label={t(($) => $.nav.users)}
    >
      <ArrowLeft aria-hidden="true" />
    </Button>
  );

  if (user.isPending) {
    return (
      <>
        <AdminPageHeader title={t(($) => $.users.detail_title)} actions={back} />
        <AdminLoading />
      </>
    );
  }
  if (user.isError) {
    return (
      <>
        <AdminPageHeader title={t(($) => $.users.detail_title)} actions={back} />
        <AdminError onRetry={() => void user.refetch()} />
      </>
    );
  }

  const u = user.data;
  const disabled = !!u.disabled_at;
  const operator = canOperate(role);

  return (
    <>
      <AdminPageHeader
        title={u.name || u.email}
        actions={
          <>
            {operator ? (
              <Button
                variant={disabled ? "default" : "destructive"}
                size="sm"
                onClick={() => setStatusDialog(true)}
              >
                {disabled ? t(($) => $.users.enable) : t(($) => $.users.disable)}
              </Button>
            ) : null}
            {back}
          </>
        }
      />
      <AdminPageBody>
        <section className="flex flex-wrap items-center gap-x-4 gap-y-1 text-caption text-muted-foreground">
          <span className="font-mono">{u.email}</span>
          <span>{formatDate(u.created_at)}</span>
          {disabled ? (
            <>
              <Badge variant="destructive">
                {t(($) => $.users.disabled_since, { when: formatDate(u.disabled_at) })}
              </Badge>
              {u.disabled_reason ? (
                <span>
                  {t(($) => $.users.disabled_reason, { reason: u.disabled_reason })}
                </span>
              ) : null}
            </>
          ) : (
            <Badge variant="outline">{t(($) => $.users.status_active)}</Badge>
          )}
        </section>

        <section className="space-y-2">
          <h2 className="text-body font-medium">
            {t(($) => $.users.workspaces_title)}
          </h2>
          {u.workspaces.length === 0 ? (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.users.workspaces_empty)}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.users.col_workspace)}</TableHead>
                  <TableHead>{t(($) => $.users.col_role)}</TableHead>
                  <TableHead>{t(($) => $.users.col_joined)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {u.workspaces.map((w) => (
                  <TableRow key={w.id}>
                    <TableCell>
                      <AppLink
                        href={paths.admin.workspace(w.id)}
                        className="hover:underline"
                      >
                        {w.name}
                        <span className="ml-2 font-mono text-caption text-muted-foreground">
                          {w.slug}
                        </span>
                      </AppLink>
                    </TableCell>
                    <TableCell>{w.role}</TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(w.joined_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </section>

        <section className="space-y-2">
          <h2 className="text-body font-medium">{t(($) => $.users.tokens_title)}</h2>
          {u.tokens.length === 0 ? (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.users.tokens_empty)}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t(($) => $.users.col_token)}</TableHead>
                  <TableHead>{t(($) => $.users.col_created)}</TableHead>
                  <TableHead>{t(($) => $.users.col_last_used)}</TableHead>
                  <TableHead>{t(($) => $.users.col_expires)}</TableHead>
                  {operator ? <TableHead /> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {u.tokens.map((tok) => (
                  <TableRow key={tok.id}>
                    <TableCell>
                      <span className="font-medium">{tok.name}</span>
                      <span className="ml-2 font-mono text-caption text-muted-foreground">
                        {tok.token_prefix}…
                      </span>
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(tok.created_at)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {tok.last_used_at
                        ? formatDate(tok.last_used_at)
                        : t(($) => $.users.never_used)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {tok.expires_at
                        ? formatDate(tok.expires_at)
                        : t(($) => $.users.no_expiry)}
                    </TableCell>
                    {operator ? (
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="xs"
                          onClick={() => setRevokeTarget(tok)}
                        >
                          {t(($) => $.users.revoke)}
                        </Button>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </section>
      </AdminPageBody>

      <ReasonDialog
        open={statusDialog}
        onOpenChange={setStatusDialog}
        destructive={!disabled}
        title={
          disabled
            ? t(($) => $.users.enable_dialog_title)
            : t(($) => $.users.disable_dialog_title)
        }
        description={
          disabled
            ? t(($) => $.users.enable_dialog_description, { email: u.email })
            : t(($) => $.users.disable_dialog_description, { email: u.email })
        }
        confirmLabel={disabled ? t(($) => $.users.enable) : t(($) => $.users.disable)}
        onConfirm={async (reason) => {
          if (disabled) {
            await enable.mutateAsync({ userId: u.id, reason });
            toast.success(t(($) => $.users.enabled_toast));
          } else {
            await disable.mutateAsync({ userId: u.id, reason });
            toast.success(t(($) => $.users.disabled_toast));
          }
        }}
      />

      <ReasonDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRevokeTarget(null);
        }}
        destructive
        title={t(($) => $.users.revoke_dialog_title)}
        description={t(($) => $.users.revoke_dialog_description, {
          prefix: revokeTarget?.token_prefix ?? "",
        })}
        confirmLabel={t(($) => $.users.revoke)}
        onConfirm={async (reason) => {
          if (!revokeTarget) return;
          await revoke.mutateAsync({ userId: u.id, tokenId: revokeTarget.id, reason });
          toast.success(t(($) => $.users.revoked_toast));
        }}
      />
    </>
  );
}

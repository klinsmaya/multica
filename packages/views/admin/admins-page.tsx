"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import {
  PLATFORM_ROLES,
  adminMeOptions,
  adminPlatformAdminsOptions,
  useAdminRemovePlatformAdmin,
  useAdminSetPlatformAdmin,
  type PlatformAdmin,
  type PlatformRole,
} from "@multica/core/admin";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { useT } from "../i18n";
import { usePlatformRoleLabel } from "./admin-shell";
import {
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  ReasonDialog,
  useAdminDate,
} from "./components/shared";

export function AdminAdminsPage() {
  const { t } = useT("admin");
  const formatDate = useAdminDate();
  const roleLabel = usePlatformRoleLabel();
  const me = useQuery(adminMeOptions());
  const admins = useQuery(adminPlatformAdminsOptions());
  const setAdmin = useAdminSetPlatformAdmin();
  const removeAdmin = useAdminRemovePlatformAdmin();

  const [addOpen, setAddOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<PlatformRole>("viewer");
  const [removeTarget, setRemoveTarget] = useState<PlatformAdmin | null>(null);

  const isSuper = me.data?.role === "super_admin";
  const roleItems = PLATFORM_ROLES.map((r) => ({ value: r, label: roleLabel(r) }));

  return (
    <>
      <AdminPageHeader
        title={t(($) => $.admins.title)}
        count={admins.data?.length}
        actions={
          isSuper ? (
            <Button size="sm" onClick={() => setAddOpen(true)}>
              <Plus className="size-4" aria-hidden="true" />
              {t(($) => $.admins.add)}
            </Button>
          ) : null
        }
      />
      <AdminPageBody>
        {admins.isPending ? (
          <AdminLoading />
        ) : admins.isError ? (
          <AdminError onRetry={() => void admins.refetch()} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.admins.col_admin)}</TableHead>
                <TableHead>{t(($) => $.admins.col_role)}</TableHead>
                <TableHead>{t(($) => $.admins.col_granted)}</TableHead>
                {isSuper ? <TableHead /> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {admins.data.map((a) => {
                const self = a.user_id === me.data?.user_id;
                return (
                  <TableRow key={a.user_id}>
                    <TableCell>
                      <span className="font-medium">{a.name || a.email}</span>
                      <span className="ml-2 text-caption text-muted-foreground">
                        {a.email}
                      </span>
                      {self ? (
                        <Badge variant="secondary" className="ml-2">
                          {t(($) => $.admins.you)}
                        </Badge>
                      ) : null}
                    </TableCell>
                    <TableCell>{roleLabel(a.role)}</TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDate(a.created_at)}
                    </TableCell>
                    {isSuper ? (
                      <TableCell className="text-right">
                        {self ? null : (
                          <Button
                            variant="ghost"
                            size="xs"
                            onClick={() => setRemoveTarget(a)}
                          >
                            {t(($) => $.admins.remove)}
                          </Button>
                        )}
                      </TableCell>
                    ) : null}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </AdminPageBody>

      <ReasonDialog
        open={addOpen}
        onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) {
            setEmail("");
            setRole("viewer");
          }
        }}
        title={t(($) => $.admins.add_dialog_title)}
        description={t(($) => $.admins.add_dialog_description)}
        confirmLabel={t(($) => $.admins.add)}
        canSubmit={email.trim().length > 0}
        onConfirm={async (reason) => {
          await setAdmin.mutateAsync({ email: email.trim(), role, reason });
          toast.success(t(($) => $.admins.saved_toast));
        }}
      >
        <div className="space-y-1.5">
          <Label htmlFor="admin-email">{t(($) => $.admins.email_label)}</Label>
          <Input
            id="admin-email"
            type="email"
            autoComplete="off"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="admin-role">{t(($) => $.admins.role_label)}</Label>
          <Select
            items={roleItems}
            value={role}
            onValueChange={(v) => setRole(v as PlatformRole)}
          >
            <SelectTrigger id="admin-role" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {roleItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </ReasonDialog>

      <ReasonDialog
        open={removeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRemoveTarget(null);
        }}
        destructive
        title={t(($) => $.admins.remove_dialog_title)}
        description={t(($) => $.admins.remove_dialog_description, {
          email: removeTarget?.email ?? "",
        })}
        confirmLabel={t(($) => $.admins.remove)}
        onConfirm={async (reason) => {
          if (!removeTarget) return;
          await removeAdmin.mutateAsync({ userId: removeTarget.user_id, reason });
          toast.success(t(($) => $.admins.removed_toast));
        }}
      />
    </>
  );
}

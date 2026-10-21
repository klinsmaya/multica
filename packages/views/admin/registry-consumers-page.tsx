"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Copy, Plus } from "lucide-react";
import { toast } from "sonner";
import {
  adminRegistryConsumersOptions,
  useAdminRegistryCreateConsumer,
  useAdminRegistryRotateConsumerKey,
  useAdminRegistrySetConsumerEnabled,
  type RegistryConsumer,
  type RegistryConsumerKey,
} from "@multica/core/admin";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { useT } from "../i18n";
import { useAdminRole } from "./admin-shell";
import {
  AdminError,
  AdminLoading,
  AdminPageBody,
  AdminPageHeader,
  ReasonDialog,
  useAdminDate,
} from "./components/shared";
import { RegistryNav } from "./components/registry-shared";

export function AdminRegistryConsumersPage() {
  const { t } = useT("admin");
  const { t: tCommon } = useT("common");
  const formatDate = useAdminDate();
  const role = useAdminRole();
  const consumers = useQuery(adminRegistryConsumersOptions());
  const createConsumer = useAdminRegistryCreateConsumer();
  const rotateKey = useAdminRegistryRotateConsumerKey();
  const setEnabled = useAdminRegistrySetConsumerEnabled();

  const [addOpen, setAddOpen] = useState(false);
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [rotateTarget, setRotateTarget] = useState<RegistryConsumer | null>(null);
  const [toggleTarget, setToggleTarget] = useState<RegistryConsumer | null>(null);
  // The key is shown exactly once, from the response that minted it.
  const [issued, setIssued] = useState<RegistryConsumerKey | null>(null);

  const isSuper = role === "super_admin";

  return (
    <>
      <AdminPageHeader
        title={t(($) => $.registry.consumers.title)}
        count={consumers.data?.length}
        actions={
          isSuper ? (
            <Button size="sm" onClick={() => setAddOpen(true)}>
              <Plus className="size-4" aria-hidden="true" />
              {t(($) => $.registry.consumers.add)}
            </Button>
          ) : null
        }
      />
      <AdminPageBody>
        <RegistryNav />
        <p className="text-caption text-muted-foreground">
          {t(($) => $.registry.consumers.intro)}
        </p>
        {consumers.isPending ? (
          <AdminLoading />
        ) : consumers.isError ? (
          <AdminError onRetry={() => void consumers.refetch()} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.registry.consumers.col_consumer)}</TableHead>
                <TableHead>{t(($) => $.registry.consumers.col_key)}</TableHead>
                <TableHead>{t(($) => $.registry.consumers.col_last_used)}</TableHead>
                <TableHead>{t(($) => $.registry.consumers.col_created)}</TableHead>
                {isSuper ? <TableHead /> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {consumers.data.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <span className="font-medium">{c.name}</span>
                    <span className="ml-2 font-mono text-caption text-muted-foreground">{c.key}</span>
                    {c.builtin ? (
                      <Badge variant="secondary" className="ml-2">
                        {t(($) => $.registry.consumers.builtin)}
                      </Badge>
                    ) : null}
                    {!c.enabled ? (
                      <Badge variant="destructive" className="ml-2">
                        {t(($) => $.registry.consumers.disabled)}
                      </Badge>
                    ) : null}
                    {c.webhook_url ? (
                      <p className="truncate text-caption text-muted-foreground">{c.webhook_url}</p>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {c.builtin
                      ? t(($) => $.registry.consumers.key_in_process)
                      : c.has_api_key
                        ? t(($) => $.registry.consumers.key_issued)
                        : t(($) => $.registry.consumers.key_none)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {c.last_used_at ? formatDate(c.last_used_at) : t(($) => $.registry.consumers.never_used)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {formatDate(c.created_at)}
                  </TableCell>
                  {isSuper ? (
                    <TableCell className="text-right whitespace-nowrap">
                      {c.builtin ? null : (
                        <>
                          <Button variant="ghost" size="xs" onClick={() => setRotateTarget(c)}>
                            {t(($) => $.registry.consumers.rotate)}
                          </Button>
                          <Button variant="ghost" size="xs" onClick={() => setToggleTarget(c)}>
                            {c.enabled
                              ? t(($) => $.registry.consumers.disable)
                              : t(($) => $.registry.consumers.enable)}
                          </Button>
                        </>
                      )}
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </AdminPageBody>

      <ReasonDialog
        open={addOpen}
        onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) {
            setKey("");
            setName("");
            setWebhookUrl("");
          }
        }}
        title={t(($) => $.registry.consumers.add_dialog_title)}
        description={t(($) => $.registry.consumers.add_dialog_description)}
        confirmLabel={t(($) => $.registry.consumers.add)}
        canSubmit={key.trim().length > 0 && name.trim().length > 0}
        onConfirm={async (reason) => {
          const result = await createConsumer.mutateAsync({
            key: key.trim(),
            name: name.trim(),
            webhook_url: webhookUrl.trim() || undefined,
            reason,
          });
          setIssued(result);
        }}
      >
        <div className="space-y-1.5">
          <Label htmlFor="registry-consumer-key">{t(($) => $.registry.consumers.key_label)}</Label>
          <Input
            id="registry-consumer-key"
            value={key}
            // eslint-disable-next-line no-restricted-syntax -- example consumer key, a technical value
            placeholder="acme-portal"
            autoComplete="off"
            onChange={(e) => setKey(e.target.value)}
          />
          <p className="text-caption text-muted-foreground">{t(($) => $.registry.consumers.key_hint)}</p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="registry-consumer-name">{t(($) => $.registry.consumers.name_label)}</Label>
          <Input id="registry-consumer-name" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="registry-consumer-webhook">{t(($) => $.registry.consumers.webhook_label)}</Label>
          <Input
            id="registry-consumer-webhook"
            type="url"
            value={webhookUrl}
            placeholder="https://"
            onChange={(e) => setWebhookUrl(e.target.value)}
          />
        </div>
      </ReasonDialog>

      <ReasonDialog
        open={rotateTarget !== null}
        onOpenChange={(open) => {
          if (!open) setRotateTarget(null);
        }}
        destructive
        title={t(($) => $.registry.consumers.rotate_dialog_title)}
        description={t(($) => $.registry.consumers.rotate_dialog_description, {
          name: rotateTarget?.name ?? "",
        })}
        confirmLabel={t(($) => $.registry.consumers.rotate)}
        onConfirm={async (reason) => {
          if (!rotateTarget) return;
          const result = await rotateKey.mutateAsync({ consumerId: rotateTarget.id, reason });
          setIssued(result);
        }}
      />

      <ReasonDialog
        open={toggleTarget !== null}
        onOpenChange={(open) => {
          if (!open) setToggleTarget(null);
        }}
        destructive={toggleTarget?.enabled === true}
        title={
          toggleTarget?.enabled
            ? t(($) => $.registry.consumers.disable_dialog_title)
            : t(($) => $.registry.consumers.enable_dialog_title)
        }
        description={
          toggleTarget?.enabled
            ? t(($) => $.registry.consumers.disable_dialog_description, { name: toggleTarget?.name ?? "" })
            : t(($) => $.registry.consumers.enable_dialog_description, { name: toggleTarget?.name ?? "" })
        }
        confirmLabel={
          toggleTarget?.enabled
            ? t(($) => $.registry.consumers.disable)
            : t(($) => $.registry.consumers.enable)
        }
        onConfirm={async (reason) => {
          if (!toggleTarget) return;
          await setEnabled.mutateAsync({
            consumerId: toggleTarget.id,
            enabled: !toggleTarget.enabled,
            reason,
          });
          toast.success(t(($) => $.registry.consumers.saved_toast));
        }}
      />

      <Dialog open={issued !== null} onOpenChange={(open) => !open && setIssued(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.registry.consumers.issued_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.registry.consumers.issued_description, { name: issued?.consumer.name ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-md bg-muted px-2 py-1.5 font-mono text-caption">
              {issued?.api_key}
            </code>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={t(($) => $.registry.consumers.copy_key)}
              onClick={() => {
                void navigator.clipboard?.writeText(issued?.api_key ?? "");
                toast.success(t(($) => $.registry.consumers.copied_toast));
              }}
            >
              <Copy aria-hidden="true" />
            </Button>
          </div>
          <DialogFooter>
            <Button onClick={() => setIssued(null)}>{tCommon(($) => $.confirm)}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

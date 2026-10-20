"use client";

import { useState, type ReactNode } from "react";
import { AlertCircle, ChevronLeft, ChevronRight, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { clientErrorMessage } from "@multica/core/api";
import type { AdminPage } from "@multica/core/admin";
import { CollectionPageState } from "../../layout/collection-page";
import { PAGE_GUTTER, PAGE_RAIL } from "../../layout/page-header";
import { useLocale, useT } from "../../i18n";

export const ADMIN_PAGE_SIZE = 25;

export function AdminPageHeader({
  title,
  count,
  actions,
}: {
  title: string;
  count?: number;
  actions?: ReactNode;
}) {
  return (
    <header
      className={cn(
        "flex h-12 shrink-0 items-center gap-2 border-b",
        PAGE_GUTTER,
      )}
    >
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <h1 className="truncate text-body font-medium">{title}</h1>
        {typeof count === "number" && count > 0 ? (
          <span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground">
            {count}
          </span>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}

export function AdminPageBody({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex-1 overflow-y-auto py-4", PAGE_GUTTER)}>
      <div className={cn(PAGE_RAIL, "space-y-6", className)}>{children}</div>
    </div>
  );
}

export function AdminLoading() {
  const { t } = useT("admin");
  return (
    <div
      role="status"
      className="flex items-center justify-center gap-2 py-16 text-caption text-muted-foreground"
    >
      <Loader2 className="size-4 animate-spin" aria-hidden="true" />
      {t(($) => $.loading)}
    </div>
  );
}

export function AdminError({ onRetry }: { onRetry?: () => void }) {
  const { t } = useT("admin");
  return (
    <CollectionPageState
      icon={AlertCircle}
      tone="destructive"
      role="alert"
      title={t(($) => $.load_failed)}
      actions={
        onRetry ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            {t(($) => $.retry)}
          </Button>
        ) : null
      }
    />
  );
}

// Absolute timestamps, in the UI language. Admin pages show when something
// happened, not how long ago: an auditor compares rows against each other.
export function useAdminDate() {
  const locale = useLocale();
  return (value: string | null | undefined): string => {
    if (!value) return "";
    const d = new Date(value);
    if (Number.isNaN(d.getTime())) return value;
    return d.toLocaleString(locale, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  };
}

export function AdminPagination<T>({
  page,
  onPageChange,
}: {
  page: AdminPage<T>;
  onPageChange: (next: number) => void;
}) {
  const { t } = useT("admin");
  if (page.total === 0) return null;
  const from = (page.page - 1) * page.page_size + 1;
  const to = Math.min(page.total, page.page * page.page_size);
  const hasPrev = page.page > 1;
  const hasNext = to < page.total;
  return (
    <div className="flex items-center justify-between gap-2 text-caption text-muted-foreground">
      <span className="tabular-nums">
        {t(($) => $.pagination.summary, { from, to, total: page.total })}
      </span>
      <div className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={!hasPrev}
          aria-label={t(($) => $.pagination.prev)}
          onClick={() => onPageChange(page.page - 1)}
        >
          <ChevronLeft aria-hidden="true" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={!hasNext}
          aria-label={t(($) => $.pagination.next)}
          onClick={() => onPageChange(page.page + 1)}
        >
          <ChevronRight aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}

// Returns the sentence a failed admin write should toast: the server's own
// 4xx message when it wrote one for the user, else a generic localized one.
export function useAdminErrorMessage() {
  const { t } = useT("admin");
  return (err: unknown) => clientErrorMessage(err) ?? t(($) => $.action_failed);
}

export interface ReasonDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  destructive?: boolean;
  /** Extra fields rendered above the reason textarea. */
  children?: ReactNode;
  /** Returns false to keep the dialog open without submitting. */
  canSubmit?: boolean;
  onConfirm: (reason: string) => Promise<void>;
}

/**
 * Every audited admin write goes through this dialog: it collects the reason
 * the server requires and only closes once the server has accepted the call,
 * so a failed action keeps the typed reason on screen.
 */
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  destructive,
  children,
  canSubmit = true,
  onConfirm,
}: ReasonDialogProps) {
  const { t } = useT("admin");
  const { t: tCommon } = useT("common");
  const errorMessage = useAdminErrorMessage();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const handleOpenChange = (next: boolean) => {
    if (pending) return;
    if (!next) {
      setReason("");
      setError(null);
    }
    onOpenChange(next);
  };

  const submit = async () => {
    const trimmed = reason.trim();
    if (!trimmed) {
      setError(t(($) => $.reason.required));
      return;
    }
    setPending(true);
    setError(null);
    try {
      await onConfirm(trimmed);
      setReason("");
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setPending(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {children}
            <div className="space-y-1.5">
              <Label htmlFor="admin-reason">{t(($) => $.reason.label)}</Label>
              <Textarea
                id="admin-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder={t(($) => $.reason.placeholder)}
                maxLength={500}
                rows={3}
                disabled={pending}
                aria-invalid={error ? true : undefined}
              />
            </div>
            {error ? (
              <p role="alert" className="text-caption text-destructive">
                {error}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => handleOpenChange(false)}
            >
              {tCommon(($) => $.cancel)}
            </Button>
            <Button
              type="submit"
              variant={destructive ? "destructive" : "default"}
              disabled={pending || !canSubmit}
              aria-busy={pending || undefined}
            >
              {pending ? (
                <Loader2 className="size-4 animate-spin" aria-hidden="true" />
              ) : null}
              {confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

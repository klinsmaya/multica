"use client";

import { useEffect, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Building2,
  LayoutDashboard,
  Plug,
  ScrollText,
  ShieldCheck,
  Users,
} from "lucide-react";
import { adminMeOptions, type PlatformRole } from "@multica/core/admin";
import { ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import {
  paths,
  resolvePostAuthDestination,
  useHasOnboarded,
} from "@multica/core/paths";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { MulticaIcon } from "@multica/ui/components/common/multica-icon";
import { cn } from "@multica/ui/lib/utils";
import { useLogout } from "../auth";
import { useT } from "../i18n";
import { AppLink, useNavigation } from "../navigation";
import { DragStrip } from "../platform";
import { AdminError } from "./components/shared";

export function usePlatformRoleLabel() {
  const { t } = useT("admin");
  return (role: PlatformRole | string): string => {
    switch (role) {
      case "super_admin":
        return t(($) => $.role.super_admin);
      case "operator":
        return t(($) => $.role.operator);
      case "viewer":
        return t(($) => $.role.viewer);
      default:
        return role;
    }
  };
}

const NAV = [
  { key: "overview", icon: LayoutDashboard, href: paths.admin.overview() },
  { key: "users", icon: Users, href: paths.admin.users() },
  { key: "workspaces", icon: Building2, href: paths.admin.workspaces() },
  { key: "connectors", icon: Plug, href: paths.admin.connectors() },
  { key: "audit", icon: ScrollText, href: paths.admin.audit() },
  { key: "admins", icon: ShieldCheck, href: paths.admin.admins() },
] as const;

function AdminNoAccess() {
  const { t } = useT("admin");
  const nav = useNavigation();
  const logout = useLogout();
  const hasOnboarded = useHasOnboarded();
  const { data: workspaces = [] } = useQuery(workspaceListOptions());
  return (
    <div className="flex min-h-svh flex-col">
      <DragStrip />
      <div className="flex flex-1 flex-col items-center justify-center gap-6 px-6 pb-12 text-center">
        <div className="space-y-2">
          <h1 className="text-display-sm font-semibold tracking-tight">
            {t(($) => $.no_access.title)}
          </h1>
          <p className="max-w-md text-muted-foreground">
            {t(($) => $.no_access.description)}
          </p>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Button
            onClick={() =>
              nav.replace(resolvePostAuthDestination(workspaces, hasOnboarded))
            }
          >
            {t(($) => $.no_access.back)}
          </Button>
          <Button variant="outline" onClick={logout}>
            {t(($) => $.no_access.sign_in_different)}
          </Button>
        </div>
      </div>
    </div>
  );
}

/**
 * Frame for every /admin page: verifies the signed-in account holds a
 * platform role, then renders the console navigation around the page.
 *
 * The role check is a query, not a store: a 403 here means the backend
 * refused, which is the only source of truth for "is this user an admin".
 * Pages below also read the role to hide writes a viewer cannot perform; the
 * server enforces the same rule, so hiding is a courtesy, not the gate.
 */
export function AdminShell({ children }: { children: ReactNode }) {
  const { t } = useT("admin");
  const roleLabel = usePlatformRoleLabel();
  const nav = useNavigation();
  const user = useAuthStore((s) => s.user);
  const isAuthLoading = useAuthStore((s) => s.isLoading);

  useEffect(() => {
    if (!isAuthLoading && !user) {
      nav.replace(`${paths.login()}?next=${encodeURIComponent(nav.pathname)}`);
    }
  }, [isAuthLoading, user, nav]);

  const me = useQuery({ ...adminMeOptions(), enabled: !!user });

  const loading = (
    <div className="flex h-svh items-center justify-center">
      <MulticaIcon className="size-6 animate-pulse" />
    </div>
  );
  if (isAuthLoading || !user) return loading;
  if (me.isPending) return loading;
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 403) {
      return <AdminNoAccess />;
    }
    return (
      <div className="flex min-h-svh flex-col">
        <DragStrip />
        <AdminError onRetry={() => void me.refetch()} />
      </div>
    );
  }

  return (
    <div className="flex h-svh flex-col">
      <DragStrip />
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-56 shrink-0 flex-col border-r bg-sidebar">
          <div className="flex h-12 items-center gap-2 border-b px-4">
            <MulticaIcon className="size-5" />
            <span className="truncate text-body font-medium">
              {t(($) => $.title)}
            </span>
          </div>
          <nav className="flex-1 space-y-0.5 p-2">
            {NAV.map(({ key, icon: Icon, href }) => {
              const active =
                nav.pathname === href || nav.pathname.startsWith(`${href}/`);
              return (
                <AppLink
                  key={key}
                  href={href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "flex items-center gap-2 rounded-md px-2 py-1.5 text-body transition-colors",
                    active
                      ? "bg-sidebar-accent text-sidebar-accent-foreground"
                      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
                  )}
                >
                  <Icon className="size-4 shrink-0" aria-hidden="true" />
                  <span className="truncate">{t(($) => $.nav[key])}</span>
                </AppLink>
              );
            })}
          </nav>
          <div className="space-y-2 border-t p-3">
            <p className="truncate text-caption text-muted-foreground">
              {t(($) => $.signed_in_as, {
                email: me.data.email,
                role: roleLabel(me.data.role),
              })}
            </p>
            <AppLink
              href={paths.root()}
              className="block text-caption text-muted-foreground underline-offset-4 hover:underline"
            >
              {t(($) => $.nav.back_to_app)}
            </AppLink>
          </div>
        </aside>
        <main className="flex min-w-0 flex-1 flex-col">{children}</main>
      </div>
    </div>
  );
}

/** The signed-in admin's role, for pages that hide writes a viewer lacks. */
export function useAdminRole(): PlatformRole {
  const me = useQuery(adminMeOptions());
  return me.data?.role ?? "viewer";
}

export function canOperate(role: PlatformRole): boolean {
  return role === "operator" || role === "super_admin";
}

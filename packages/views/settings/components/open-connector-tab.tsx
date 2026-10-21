"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { AlertTriangle, Check, Loader2, Pencil, Plug, Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { api, errorCode } from "@multica/core/api";
import {
  openConnectorAppsOptions,
  openConnectorConnectionsOptions,
  openConnectorKeys,
  useConnectOpenConnectorApiKey,
  useDeleteOpenConnectorConnection,
  useUpdateOpenConnectorConnection,
} from "@multica/core/connectors";
import type { OpenConnectorApp, OpenConnectorConnection } from "@multica/core/types";
import { useT, useTimeAgo } from "../../i18n";
import { useNavigation } from "../../navigation";

const CONNECT_FAILED_ERROR = "open_connector_connect_failed";
const CONNECT_BUSY_CODE = "open_connector_connect_busy";

function supportsOAuth(app: OpenConnectorApp) {
  return app.auth_types.includes("oauth") || app.auth_types.includes("oauth2");
}

function supportsApiKey(app: OpenConnectorApp) {
  return app.auth_types.includes("api_key");
}

function AppLogo({ app }: { app: { name: string; service: string; logo?: string } }) {
  const [failed, setFailed] = useState(false);
  const initial = (app.name || app.service).charAt(0).toUpperCase();
  if (app.logo && !failed) {
    return (
      <img
        src={app.logo}
        alt=""
        className="h-8 w-8 shrink-0 rounded-xs bg-muted object-contain"
        onError={() => setFailed(true)}
      />
    );
  }
  return (
    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-xs bg-muted text-caption font-semibold text-muted-foreground">
      {initial}
    </div>
  );
}

/**
 * Connect, manage and disconnect open-connector apps. Connections belong to the
 * signed-in user. `ownsConnectedCallback` is true when no other provider
 * section on the page consumes the shared `?connected=<service>` return param
 * (the param carries no provider marker, so exactly one section toasts and
 * strips it); this tab always refreshes its own connection list for it.
 */
export function OpenConnectorTab({
  ownsConnectedCallback = true,
}: {
  ownsConnectedCallback?: boolean;
}) {
  const { t } = useT("settings");
  const timeAgo = useTimeAgo();
  const qc = useQueryClient();
  const navigation = useNavigation();

  const appsQuery = useQuery(openConnectorAppsOptions());
  const connectionsQuery = useQuery(openConnectorConnectionsOptions());
  const updateConnection = useUpdateOpenConnectorConnection();
  const deleteConnection = useDeleteOpenConnectorConnection();

  const [query, setQuery] = useState("");
  const [connectingService, setConnectingService] = useState<string | null>(null);
  const [apiKeyApp, setApiKeyApp] = useState<OpenConnectorApp | null>(null);
  const [renameTarget, setRenameTarget] = useState<OpenConnectorConnection | null>(null);
  const [disconnectTarget, setDisconnectTarget] = useState<{
    connection: OpenConnectorConnection;
    name: string;
  } | null>(null);

  // Return from the OAuth consent flow: `?connected=<service>` or
  // `?error=open_connector_connect_failed`. Consume once (StrictMode runs
  // effects twice), refresh the list, and strip the one-shot params.
  const connectedParam = navigation.searchParams.get("connected");
  const errorParam = navigation.searchParams.get("error");
  const consumedKey = useRef<string | null>(null);
  useEffect(() => {
    const failed = errorParam === CONNECT_FAILED_ERROR;
    const key = failed ? `error:${CONNECT_FAILED_ERROR}` : connectedParam ? `connected:${connectedParam}` : null;
    if (!key || consumedKey.current === key) return;
    consumedKey.current = key;
    if (connectedParam) void qc.invalidateQueries({ queryKey: openConnectorKeys.connections() });
    if (!failed && !ownsConnectedCallback) return;
    if (failed) toast.error(t(($) => $.open_connector.toast_connect_failed));
    else toast.success(t(($) => $.open_connector.toast_connected));
    const params = new URLSearchParams(navigation.searchParams);
    params.set("tab", "apps");
    params.delete("integration");
    params.delete("connected");
    if (failed) params.delete("error");
    const qs = params.toString();
    navigation.replace(qs ? `${navigation.pathname}?${qs}` : navigation.pathname);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connectedParam, errorParam]);

  const apps = useMemo(() => appsQuery.data ?? [], [appsQuery.data]);
  const appByService = useMemo(() => new Map(apps.map((a) => [a.service, a])), [apps]);
  const nameOf = (service: string) => appByService.get(service)?.name || service;

  const connections = useMemo(() => connectionsQuery.data ?? [], [connectionsQuery.data]);
  const groups = useMemo(() => {
    const m = new Map<string, OpenConnectorConnection[]>();
    for (const c of connections) m.set(c.service, [...(m.get(c.service) ?? []), c]);
    return Array.from(m.entries());
  }, [connections]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return apps;
    return apps.filter(
      (a) => a.name.toLowerCase().includes(q) || a.service.toLowerCase().includes(q),
    );
  }, [apps, query]);

  function connectErrorMessage(e: unknown, fallback: string) {
    if (errorCode(e) === CONNECT_BUSY_CODE) return t(($) => $.open_connector.connect_busy);
    return fallback;
  }

  async function handleOAuthConnect(service: string) {
    if (connectingService) return;
    setConnectingService(service);
    try {
      const { redirect_url } = await api.openConnectorConnectInit(service);
      if (!redirect_url) throw new Error("empty redirect_url");
      window.location.href = redirect_url;
    } catch (e) {
      toast.error(connectErrorMessage(e, t(($) => $.open_connector.connect_failed)));
      setConnectingService(null);
    }
  }

  async function handleSetDefault(c: OpenConnectorConnection) {
    try {
      await updateConnection.mutateAsync({ id: c.id, is_default: true });
    } catch {
      toast.error(t(($) => $.open_connector.update_failed));
    }
  }

  async function handleDisconnect() {
    if (!disconnectTarget || deleteConnection.isPending) return;
    try {
      await deleteConnection.mutateAsync(disconnectTarget.connection.id);
      toast.success(t(($) => $.open_connector.toast_disconnected));
      setDisconnectTarget(null);
    } catch {
      toast.error(t(($) => $.open_connector.disconnect_failed));
    }
  }

  const connectBusy = connectingService !== null;

  return (
    <div className="space-y-6">
      {connectionsQuery.isError && (
        <p className="text-caption text-destructive">
          {t(($) => $.open_connector.connections_load_failed)}
        </p>
      )}

      {groups.length > 0 && (
        <section className="space-y-3">
          <h3 className="text-body font-medium">{t(($) => $.open_connector.connections_title)}</h3>
          <div className="space-y-2">
            {groups.map(([service, items]) => (
              <Card key={service}>
                <CardContent className="space-y-2 p-3">
                  <div className="flex items-center gap-3">
                    <AppLogo app={appByService.get(service) ?? { name: service, service }} />
                    <p className="min-w-0 flex-1 truncate text-body font-medium">{nameOf(service)}</p>
                  </div>
                  <ul className="divide-y">
                    {items.map((c) => (
                      <ConnectionRow
                        key={c.id}
                        connection={c}
                        displayName={c.label || nameOf(c.service)}
                        timeAgo={timeAgo}
                        busy={updateConnection.isPending}
                        canReconnect={
                          c.status === "reauth_required" &&
                          c.auth_type === "oauth" &&
                          (appByService.get(c.service)
                            ? supportsOAuth(appByService.get(c.service)!)
                            : false)
                        }
                        reconnecting={connectingService === c.service}
                        connectBusy={connectBusy}
                        onReconnect={() => handleOAuthConnect(c.service)}
                        onSetDefault={() => handleSetDefault(c)}
                        onRename={() => setRenameTarget(c)}
                        onDisconnect={() =>
                          setDisconnectTarget({
                            connection: c,
                            name: nameOf(c.service),
                          })
                        }
                      />
                    ))}
                  </ul>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}

      {appsQuery.isLoading ? (
        <p className="text-body text-muted-foreground">{t(($) => $.open_connector.loading)}</p>
      ) : appsQuery.isError ? (
        <p className="text-body text-destructive">{t(($) => $.open_connector.load_failed)}</p>
      ) : apps.length === 0 ? (
        <p className="text-body font-medium">{t(($) => $.open_connector.empty_title)}</p>
      ) : (
        <section className="space-y-3">
          <h3 className="text-body font-medium">{t(($) => $.open_connector.apps_title)}</h3>
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t(($) => $.open_connector.search_placeholder)}
            className="max-w-xs"
          />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {filtered.map((app) => (
              <Card key={app.service}>
                <CardContent className="flex flex-wrap items-center gap-3 p-3">
                  <AppLogo app={app} />
                  <p className="min-w-0 flex-1 truncate text-body font-medium">
                    {app.name || app.service}
                  </p>
                  <div className="flex flex-wrap items-center gap-2">
                    {supportsOAuth(app) && (
                      <Button
                        size="sm"
                        onClick={() => handleOAuthConnect(app.service)}
                        disabled={connectBusy}
                      >
                        {connectingService === app.service ? (
                          <Loader2 className="h-3 w-3 animate-spin" />
                        ) : (
                          <Plug className="h-3 w-3" />
                        )}
                        {connectingService === app.service
                          ? t(($) => $.open_connector.connecting)
                          : t(($) => $.open_connector.connect)}
                      </Button>
                    )}
                    {supportsApiKey(app) && (
                      <Button
                        size="sm"
                        variant={supportsOAuth(app) ? "outline" : "default"}
                        onClick={() => setApiKeyApp(app)}
                        disabled={connectBusy}
                      >
                        {t(($) => $.open_connector.connect_api_key)}
                      </Button>
                    )}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}

      <ApiKeyDialog
        app={apiKeyApp}
        onClose={() => setApiKeyApp(null)}
        connectErrorMessage={connectErrorMessage}
      />

      <RenameDialog
        connection={renameTarget}
        fallbackName={renameTarget ? nameOf(renameTarget.service) : ""}
        onClose={() => setRenameTarget(null)}
      />

      <AlertDialog
        open={!!disconnectTarget}
        onOpenChange={(v) => {
          if (!v && !deleteConnection.isPending) setDisconnectTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.open_connector.disconnect_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.open_connector.disconnect_confirm_description, {
                name: disconnectTarget?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteConnection.isPending}>
              {t(($) => $.open_connector.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleDisconnect} disabled={deleteConnection.isPending}>
              {deleteConnection.isPending
                ? t(($) => $.open_connector.disconnecting)
                : t(($) => $.open_connector.disconnect)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function ConnectionRow({
  connection: c,
  displayName,
  timeAgo,
  busy,
  canReconnect,
  reconnecting,
  connectBusy,
  onReconnect,
  onSetDefault,
  onRename,
  onDisconnect,
}: {
  connection: OpenConnectorConnection;
  displayName: string;
  timeAgo: (iso: string) => string;
  busy: boolean;
  canReconnect: boolean;
  reconnecting: boolean;
  connectBusy: boolean;
  onReconnect: () => void;
  onSetDefault: () => void;
  onRename: () => void;
  onDisconnect: () => void;
}) {
  const { t } = useT("settings");
  return (
    <li className="flex flex-wrap items-center gap-2 py-2">
      <div className="min-w-0 flex-1">
        <p className="flex items-center gap-2 truncate text-body">
          <span className="truncate">{displayName}</span>
          {c.is_default && (
            <span className="shrink-0 rounded-sm bg-muted px-1.5 text-micro text-muted-foreground">
              {t(($) => $.open_connector.default_badge)}
            </span>
          )}
        </p>
        <p className="truncate text-micro text-muted-foreground">
          {c.last_used_at
            ? t(($) => $.open_connector.last_used, { when: timeAgo(c.last_used_at) })
            : t(($) => $.open_connector.last_used_never)}
        </p>
      </div>
      {c.status === "active" ? (
        <span className="inline-flex items-center gap-1 text-caption text-emerald-600">
          <Check className="h-3 w-3" />
          {t(($) => $.open_connector.status_active)}
        </span>
      ) : c.status === "reauth_required" ? (
        <span className="inline-flex items-center gap-1 text-caption text-amber-600">
          <AlertTriangle className="h-3 w-3" />
          {t(($) => $.open_connector.status_reauth)}
        </span>
      ) : (
        <span className="text-caption text-muted-foreground">
          {t(($) => $.open_connector.status_unknown)}
        </span>
      )}
      <div className="flex items-center gap-1">
        {canReconnect && (
          <Button size="sm" variant="outline" onClick={onReconnect} disabled={connectBusy}>
            {reconnecting ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
            {t(($) => $.open_connector.reconnect)}
          </Button>
        )}
        {!c.is_default && (
          <Button size="sm" variant="outline" onClick={onSetDefault} disabled={busy}>
            {t(($) => $.open_connector.set_default)}
          </Button>
        )}
        <Button
          size="icon-sm"
          variant="outline"
          onClick={onRename}
          aria-label={`${t(($) => $.open_connector.rename)} ${displayName}`}
        >
          <Pencil className="h-3 w-3" />
        </Button>
        <Button
          size="icon-sm"
          variant="outline"
          onClick={onDisconnect}
          aria-label={`${t(($) => $.open_connector.disconnect)} ${displayName}`}
        >
          <Trash2 className="h-3 w-3" />
        </Button>
      </div>
    </li>
  );
}

function ApiKeyDialog({
  app,
  onClose,
  connectErrorMessage,
}: {
  app: OpenConnectorApp | null;
  onClose: () => void;
  connectErrorMessage: (e: unknown, fallback: string) => string;
}) {
  const { t } = useT("settings");
  const connect = useConnectOpenConnectorApiKey();
  const [apiKey, setApiKey] = useState("");
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | null>(null);

  // Drop the secret as soon as the dialog closes or switches app.
  useEffect(() => {
    setApiKey("");
    setLabel("");
    setError(null);
  }, [app?.service]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!app || !apiKey.trim() || connect.isPending) return;
    setError(null);
    try {
      await connect.mutateAsync({
        service: app.service,
        api_key: apiKey.trim(),
        ...(label.trim() ? { label: label.trim() } : {}),
      });
      toast.success(t(($) => $.open_connector.toast_connected));
      setApiKey("");
      onClose();
    } catch (err) {
      setError(connectErrorMessage(err, t(($) => $.open_connector.api_key_connect_failed)));
    }
  }

  return (
    <Dialog
      open={!!app}
      onOpenChange={(v) => {
        if (!v && !connect.isPending) onClose();
      }}
    >
      <DialogContent>
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>
              {t(($) => $.open_connector.api_key_title, { name: app?.name || app?.service || "" })}
            </DialogTitle>
          </DialogHeader>
          <label className="block space-y-1.5 text-caption font-medium">
            {t(($) => $.open_connector.api_key_label)}
            <Input
              type="password"
              autoComplete="off"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </label>
          <label className="block space-y-1.5 text-caption font-medium">
            {t(($) => $.open_connector.label_label)}
            <Input value={label} maxLength={80} onChange={(e) => setLabel(e.target.value)} />
          </label>
          {error && (
            <p role="alert" className="text-caption text-destructive">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={connect.isPending}>
              {t(($) => $.open_connector.cancel)}
            </Button>
            <Button type="submit" disabled={!apiKey.trim() || connect.isPending} aria-busy={connect.isPending}>
              {connect.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
              {t(($) => $.open_connector.submit)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RenameDialog({
  connection,
  fallbackName,
  onClose,
}: {
  connection: OpenConnectorConnection | null;
  fallbackName: string;
  onClose: () => void;
}) {
  const { t } = useT("settings");
  const update = useUpdateOpenConnectorConnection();
  const [label, setLabel] = useState("");
  const [error, setError] = useState(false);

  useEffect(() => {
    setLabel(connection?.label ?? "");
    setError(false);
  }, [connection?.id, connection?.label]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!connection || update.isPending) return;
    setError(false);
    try {
      await update.mutateAsync({ id: connection.id, label: label.trim() });
      onClose();
    } catch {
      setError(true);
    }
  }

  return (
    <Dialog
      open={!!connection}
      onOpenChange={(v) => {
        if (!v && !update.isPending) onClose();
      }}
    >
      <DialogContent>
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{t(($) => $.open_connector.rename_title)}</DialogTitle>
          </DialogHeader>
          <label className="block space-y-1.5 text-caption font-medium">
            {t(($) => $.open_connector.rename_label)}
            <Input
              value={label}
              maxLength={80}
              placeholder={fallbackName}
              onChange={(e) => setLabel(e.target.value)}
            />
          </label>
          {error && (
            <p role="alert" className="text-caption text-destructive">
              {t(($) => $.open_connector.rename_failed)}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={update.isPending}>
              {t(($) => $.open_connector.cancel)}
            </Button>
            <Button type="submit" disabled={update.isPending} aria-busy={update.isPending}>
              {update.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
              {t(($) => $.open_connector.save)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

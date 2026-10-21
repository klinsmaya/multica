"use client";

import { useQuery } from "@tanstack/react-query";
import { ApiError, errorCode } from "@multica/core/api";
import { composioToolkitsOptions } from "@multica/core/composio";
import { useFeatureEnabled } from "@multica/core/config";
import { openConnectorAppsOptions } from "@multica/core/connectors";
import {
  COMPOSIO_MCP_APPS_FLAG,
  OPEN_CONNECTOR_APPS_FLAG,
} from "@multica/core/feature-flags";
import { useT } from "../../i18n";
import { ComposioTab } from "./composio-tab";
import { OpenConnectorTab } from "./open-connector-tab";
import { SettingsTab } from "./settings-layout";

/**
 * Whether the Connected apps page exists. A pending or failed catalog read
 * still counts as available — only an explicit "Composio is not configured on
 * this server" hides the page, so a transient error never removes it.
 */
export function useComposioAvailable(): boolean {
  const enabled = useFeatureEnabled(COMPOSIO_MCP_APPS_FLAG, false);
  const toolkits = useQuery({ ...composioToolkitsOptions(), enabled });
  return (
    enabled &&
    errorCode(toolkits.error) !== "composio_not_configured" &&
    !(toolkits.error instanceof ApiError && toolkits.error.status === 503)
  );
}

/**
 * Whether the open-connector section exists. Independent of Composio: only an
 * explicit `open_connector_not_configured` hides it; a pending or failed
 * catalog read keeps it so a transient error never removes the page.
 */
export function useOpenConnectorAvailable(): boolean {
  const enabled = useFeatureEnabled(OPEN_CONNECTOR_APPS_FLAG, false);
  const apps = useQuery({ ...openConnectorAppsOptions(), enabled });
  return enabled && errorCode(apps.error) !== "open_connector_not_configured";
}

/** Whether any app provider is available, i.e. whether the page exists. */
export function useConnectedAppsAvailable(): boolean {
  const composio = useComposioAvailable();
  const openConnector = useOpenConnectorAvailable();
  return composio || openConnector;
}

/**
 * App connections belong to the signed-in account, not the workspace: the
 * user authorizes their own Gmail or Notion and enables it on agents they own.
 * Providers are separate sections with independent availability.
 */
export function ConnectedAppsTab() {
  const { t } = useT("settings");
  const composioAvailable = useComposioAvailable();
  const openConnectorAvailable = useOpenConnectorAvailable();
  const both = composioAvailable && openConnectorAvailable;
  return (
    <SettingsTab
      title={t(($) => $.page.tabs.apps)}
      description={t(($) => $.composio.page_description)}
      scope="account"
    >
      <div className="space-y-8">
        {composioAvailable && (
          <section className="space-y-3">
            {both && <h2 className="text-body font-semibold">{t(($) => $.open_connector.composio_heading)}</h2>}
            <ComposioTab />
          </section>
        )}
        {openConnectorAvailable && (
          <section className="space-y-3">
            {both && <h2 className="text-body font-semibold">{t(($) => $.open_connector.heading)}</h2>}
            <OpenConnectorTab ownsConnectedCallback={!composioAvailable} />
          </section>
        )}
      </div>
    </SettingsTab>
  );
}

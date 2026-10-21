import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ProjectPullRequestState } from "../types";

export const projectKeys = {
  all: (wsId: string) => ["projects", wsId] as const,
  list: (wsId: string) => [...projectKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...projectKeys.all(wsId), "detail", id] as const,
};

export function projectListOptions(wsId: string) {
  return queryOptions({
    queryKey: projectKeys.list(wsId),
    queryFn: () => api.listProjects(),
    select: (data) => data.projects,
  });
}

export function projectDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: projectKeys.detail(wsId, id),
    queryFn: () => api.getProject(id),
  });
}

export interface ProjectPullRequestsParams {
  state?: ProjectPullRequestState;
  limit?: number;
  offset?: number;
}

export const projectPullRequestKeys = {
  /** Prefix of every project's PR lists; the `pull_request` realtime event
   * invalidates it because a PR can belong to any project. */
  all: (wsId: string) => [...projectKeys.all(wsId), "pull-requests"] as const,
  list: (wsId: string, projectId: string, params: ProjectPullRequestsParams) =>
    [...projectPullRequestKeys.all(wsId), projectId, params] as const,
};

export function projectPullRequestsOptions(
  wsId: string,
  projectId: string,
  params: ProjectPullRequestsParams,
) {
  return queryOptions({
    queryKey: projectPullRequestKeys.list(wsId, projectId, params),
    queryFn: () => api.listProjectPullRequests(projectId, params),
  });
}

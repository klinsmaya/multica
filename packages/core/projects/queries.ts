import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ProjectPullRequestState } from "../types";
import type { ProjectAttachmentsParams } from "../types/project-library";

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

export const projectAttachmentKeys = {
  /** Every project's library in the workspace; the realtime invalidation prefix. */
  all: (wsId: string) => [...projectKeys.all(wsId), "attachments"] as const,
  list: (wsId: string, projectId: string, params: ProjectAttachmentsParams = {}) =>
    [...projectAttachmentKeys.all(wsId), projectId, params] as const,
};

/** The project library: attachments of every issue in the project, newest first. */
export function projectAttachmentsOptions(
  wsId: string,
  projectId: string,
  params: ProjectAttachmentsParams = {},
) {
  return queryOptions({
    queryKey: projectAttachmentKeys.list(wsId, projectId, params),
    queryFn: () => api.listProjectAttachments(projectId, params),
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

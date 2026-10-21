"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { issueKeys } from "@multica/core/issues/queries";
import { paths, useCurrentWorkspace, useWorkspaceSlug } from "@multica/core/paths";
import type { ReplyAnnotation } from "@multica/core/drafts/reply-annotation";
import type { Issue } from "@multica/core/types";
import { useOptionalNavigation } from "../../navigation/context";

/**
 * Resolver for `composeAnnotatedReply`: the `#comment-<id>` deep link that
 * `IssueDetailRoute` scrolls to and highlights, built like the issue's
 * "copy comment link" action. Composers can mount outside a workspace route
 * or navigation provider, where it degrades to "no link".
 */
export function useAnnotationSourceLink(issueId: string) {
  const queryClient = useQueryClient();
  const slug = useWorkspaceSlug();
  const wsId = useCurrentWorkspace()?.id;
  const navigation = useOptionalNavigation();

  return useCallback(
    (annotation: ReplyAnnotation) => {
      if (!slug || !navigation) return null;
      // Cache-only read: the identifier form is the shareable one, and the
      // composer never needs to fetch the issue just to label a quote.
      const identifier = wsId
        ? queryClient.getQueryData<Issue>(issueKeys.detail(wsId, issueId))?.identifier
        : undefined;
      const issueUrl = navigation.getShareableUrl(paths.workspace(slug).issueDetail(identifier || issueId));
      return `${issueUrl}#comment-${annotation.sourceCommentId}`;
    },
    [queryClient, slug, wsId, navigation, issueId],
  );
}

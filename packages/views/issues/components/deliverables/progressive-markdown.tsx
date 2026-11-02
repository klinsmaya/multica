"use client";

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Attachment } from "@multica/core/types";
import {
  pendingTitle,
  splitParagraphs,
} from "@multica/core/attachments/progressive-document";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n";
import { ReadonlyContent } from "../../../editor/readonly-content";

type Segment =
  | { kind: "markdown"; key: string; text: string }
  | { kind: "pending"; key: string; title: string }
  | { kind: "changed"; key: string; text: string };

// Blocks that read the same in any renderer go back together, so lists,
// footnotes and reference links keep working across them; only placeholders
// and freshly written blocks are cut out.
function toSegments(text: string, changed: ReadonlySet<number>): Segment[] {
  const segments: Segment[] = [];
  let plain: string[] = [];
  let fresh: string[] = [];
  const flushPlain = () => {
    if (plain.length > 0) {
      segments.push({ kind: "markdown", key: `m${segments.length}`, text: plain.join("\n\n") });
    }
    plain = [];
  };
  const flushFresh = () => {
    if (fresh.length > 0) {
      segments.push({ kind: "changed", key: `c${segments.length}`, text: fresh.join("\n\n") });
    }
    fresh = [];
  };
  splitParagraphs(text).forEach((block, index) => {
    const title = pendingTitle(block);
    if (title !== null) {
      flushPlain();
      flushFresh();
      segments.push({ kind: "pending", key: `p${segments.length}`, title });
    } else if (changed.has(index)) {
      flushPlain();
      fresh.push(block);
    } else {
      flushFresh();
      plain.push(block);
    }
  });
  flushPlain();
  flushFresh();
  return segments;
}

const AT_BOTTOM_SLACK_PX = 48;

function scrollParentOf(el: HTMLElement): HTMLElement | null {
  for (let node = el.parentElement; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY;
    if (overflow === "auto" || overflow === "scroll") return node;
  }
  return null;
}

function PendingSection({ title }: { title: string }) {
  const { t } = useT("issues");
  return (
    <div
      data-pending-section
      className="flex items-center justify-between gap-3 rounded-lg border border-dashed border-border px-4 py-3 text-body text-muted-foreground"
    >
      <span className="min-w-0 truncate">{title}</span>
      <span className="shrink-0 rounded-xs bg-muted px-1 text-micro font-medium">
        {t(($) => $.deliverables.progressive.pending)}
      </span>
    </div>
  );
}

function ChangedBlock({
  text,
  attachments,
  first,
}: {
  text: string;
  attachments: Attachment[];
  first: boolean;
}) {
  const { t } = useT("issues");
  // Born tinted, then released on the next frame so the transition runs.
  const [lit, setLit] = useState(true);
  useEffect(() => {
    const frame = requestAnimationFrame(() => setLit(false));
    return () => cancelAnimationFrame(frame);
  }, []);
  return (
    <div
      data-changed-paragraph
      data-first-change={first ? "" : undefined}
      className={cn(
        "relative -mx-3 rounded-md px-3 transition-colors duration-[2000ms] ease-out",
        lit ? "bg-brand/15" : "bg-transparent",
      )}
    >
      <span className="absolute -top-2 right-2 rounded-xs bg-brand px-1 text-micro font-medium text-brand-foreground">
        {t(($) => $.deliverables.progressive.changed_label)}
      </span>
      <ReadonlyContent content={text} attachments={attachments} />
    </div>
  );
}

/**
 * A Markdown body for a document an agent writes section by section: each
 * `<!-- pending: Title -->` line is a placeholder box, and the paragraphs a
 * new version added (`changedIndexes`, into `splitParagraphs(text)`) carry a
 * label and a fading highlight. A document with neither renders exactly as a
 * plain `ReadonlyContent` would.
 */
export function ProgressiveMarkdown({
  text,
  changedIndexes,
  className,
  attachments,
}: {
  text: string;
  changedIndexes: readonly number[];
  className?: string;
  attachments: Attachment[];
}) {
  const changed = useMemo(() => new Set(changedIndexes), [changedIndexes]);
  const segments = useMemo(() => toSegments(text, changed), [text, changed]);
  const root = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const plain = segments.length <= 1 && segments.every((s) => s.kind === "markdown");

  // Whether the reader sits at the end, from the last time they moved: the
  // new text has already grown the page by the time the effect below runs.
  useEffect(() => {
    const el = root.current && scrollParentOf(root.current);
    if (!el) return;
    const measure = () => {
      atBottom.current =
        el.scrollHeight - el.scrollTop - el.clientHeight <= AT_BOTTOM_SLACK_PX;
    };
    measure();
    el.addEventListener("scroll", measure, { passive: true });
    return () => el.removeEventListener("scroll", measure);
  }, [plain]);

  const changeKey = changedIndexes.join(",");
  useLayoutEffect(() => {
    if (!changeKey || !atBottom.current) return;
    root.current
      ?.querySelector("[data-first-change]")
      ?.scrollIntoView?.({ block: "start", behavior: "smooth" });
  }, [changeKey, text]);

  if (plain) {
    return <ReadonlyContent content={text} className={className} attachments={attachments} />;
  }

  let firstChange = true;
  return (
    <div ref={root} className={cn("flex flex-col gap-4", className)}>
      {segments.map((segment) => {
        if (segment.kind === "pending") {
          return <PendingSection key={segment.key} title={segment.title} />;
        }
        if (segment.kind === "changed") {
          const first = firstChange;
          firstChange = false;
          return (
            <ChangedBlock
              key={segment.key}
              text={segment.text}
              attachments={attachments}
              first={first}
            />
          );
        }
        return <ReadonlyContent key={segment.key} content={segment.text} attachments={attachments} />;
      })}
    </div>
  );
}

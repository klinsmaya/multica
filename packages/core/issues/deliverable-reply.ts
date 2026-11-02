import { attachmentDownloadPath } from "../types/attachment-url";

/** The deliverable a reply is written about. */
export interface DeliverableReplyRef {
  attachmentId: string;
  filename: string;
}

/** Escape the characters that end a Markdown link label early. */
function escapeLabel(text: string): string {
  return text.replace(/[[\]\\()]/g, (char) => `\\${char}`);
}

/**
 * Prefix a reply with a file card for the deliverable it is about. The card
 * uses the stable per-attachment download path, which the server resolves on
 * every request, so the reference outlives signed URLs.
 *
 * `/note` must stay the first token so the reference cannot change triggering.
 */
export function composeDeliverableReply(
  content: string,
  ref: DeliverableReplyRef | null | undefined,
): string {
  if (!ref) return content;
  const body = content.trim();
  const label = escapeLabel(ref.filename.replace(/\s+/g, " ").trim() || "file");
  const card = `!file[${label}](${attachmentDownloadPath(ref.attachmentId)})`;
  const command = body.match(/^\/note(?=\s|$)/i)?.[0];
  const rest = command ? body.slice(command.length).trim() : body;
  return [command, card, rest].filter(Boolean).join("\n\n");
}

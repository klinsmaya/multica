import { quoteMarkdown } from "../drafts/reply-annotation";

/**
 * Body of one comment sent from the feed composer to a waiting thread: a
 * quoted origin marker, then the message. A leading `/note` stays the first
 * token, as in annotated replies, so the marker cannot change triggering.
 */
export function composeFeedForwardComment(content: string, marker: string): string {
  const body = content.trim();
  const command = body.match(/^\/note(?=\s|$)/i)?.[0];
  const rest = command ? body.slice(command.length).trim() : body;
  const quoted = quoteMarkdown(marker);
  const message = rest ? `${quoted}\n\n${rest}` : quoted;
  return command ? `${command}\n\n${message}` : message;
}

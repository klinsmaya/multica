import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithI18n } from "../../../test/i18n";
import { ProgressiveMarkdown } from "./progressive-markdown";

function renderDoc(text: string, changedIndexes: number[] = []) {
  return renderWithI18n(
    <ProgressiveMarkdown text={text} changedIndexes={changedIndexes} attachments={[]} />,
  );
}

describe("ProgressiveMarkdown", () => {
  it("shows a pending marker as a placeholder box with its title", () => {
    const { container } = renderDoc(
      "# Report\n\nIntro text.\n\n<!-- pending: Findings -->\n\n<!-- pending: Next steps -->",
    );

    const boxes = container.querySelectorAll("[data-pending-section]");
    expect(boxes).toHaveLength(2);
    expect(boxes[0]).toHaveTextContent("Findings");
    expect(boxes[0]).toHaveTextContent("Pending");
    expect(boxes[1]).toHaveTextContent("Next steps");
    expect(screen.getByText("Intro text.")).toBeInTheDocument();
    expect(container).not.toHaveTextContent("pending:");
  });

  it("renders a document without markers as plain Markdown", () => {
    const { container } = renderDoc("# Title\n\nJust a paragraph.");

    expect(screen.getByRole("heading", { name: "Title" })).toBeInTheDocument();
    expect(screen.getByText("Just a paragraph.")).toBeInTheDocument();
    expect(container.querySelector("[data-pending-section]")).toBeNull();
    expect(container.querySelector("[data-changed-paragraph]")).toBeNull();
    expect(screen.queryByText("Claude")).not.toBeInTheDocument();
  });

  it("labels the paragraphs a new version added", () => {
    // Blocks: 0 heading, 1 new text, 2 marker.
    const { container } = renderDoc(
      "# Report\n\nBackground, now written.\n\n<!-- pending: Findings -->",
      [1],
    );

    const changed = container.querySelectorAll("[data-changed-paragraph]");
    expect(changed).toHaveLength(1);
    expect(changed[0]).toHaveTextContent("Background, now written.");
    expect(changed[0]).toHaveTextContent("Claude");
    expect(screen.getAllByText("Claude")).toHaveLength(1);
    expect(screen.getByRole("heading", { name: "Report" })).toBeInTheDocument();
  });

  it("localizes the placeholder label", () => {
    const { container } = renderWithI18n(
      <ProgressiveMarkdown text={"<!-- pending: 结论 -->"} changedIndexes={[]} attachments={[]} />,
      { locale: "zh-Hans" },
    );
    expect(container.querySelector("[data-pending-section]")).toHaveTextContent("待写");
  });
});

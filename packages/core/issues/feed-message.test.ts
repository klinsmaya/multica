// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseFeedMessage } from "./feed-message";

const AGENT = "11111111-1111-4111-8111-111111111111";
const SQUAD = "22222222-2222-4222-8222-222222222222";
const MEMBER = "33333333-3333-4333-8333-333333333333";

describe("parseFeedMessage", () => {
  it("returns null for blank input", () => {
    expect(parseFeedMessage("")).toBeNull();
    expect(parseFeedMessage(" \n\n  ")).toBeNull();
  });

  it("uses the first line as the title and the rest as the description", () => {
    expect(parseFeedMessage("Fix login\n\nSteps:\n- open page")).toEqual({
      title: "Fix login",
      description: "Steps:\n- open page",
      assignee: null,
    });
  });

  it("skips leading blank lines and omits an empty description", () => {
    expect(parseFeedMessage("\n\n  Only a title  \n\n")).toEqual({
      title: "Only a title",
      description: undefined,
      assignee: null,
    });
  });

  it("assigns the first @agent or @squad mention", () => {
    const body = `Ship it [@Go](mention://agent/${AGENT})\n\ncc [@Core](mention://squad/${SQUAD})`;
    expect(parseFeedMessage(body)?.assignee).toEqual({ type: "agent", id: AGENT });
    expect(
      parseFeedMessage(`[@Core](mention://squad/${SQUAD}) go\n\nplease`)?.assignee,
    ).toEqual({ type: "squad", id: SQUAD });
  });

  it("ignores member mentions for assignment", () => {
    expect(
      parseFeedMessage(`Hi [@Ann](mention://member/${MEMBER})`)?.assignee,
    ).toBeNull();
  });

  it("renders a title mention as plain text and keeps the markup in the description", () => {
    const parsed = parseFeedMessage(
      `[@Go](mention://agent/${AGENT}) review\n\nlook at [@Go](mention://agent/${AGENT})`,
    );
    expect(parsed?.title).toBe("@Go review");
    expect(parsed?.description).toBe(`look at [@Go](mention://agent/${AGENT})`);
  });
});

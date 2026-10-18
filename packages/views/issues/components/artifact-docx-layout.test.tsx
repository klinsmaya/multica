import { renderAsync } from "docx-preview";
import { describe, expect, it } from "vitest";

// Two-page package. The bytes are a stored zip, not an extracted-text preview.
const sampleDocx = Uint8Array.from(atob(
  "UEsDBBQAAAAAADpZUl3XeYTquAEAALgBAAATAAAAW0NvbnRlbnRfVHlwZXNdLnhtbDw/eG1sIHZlcnNpb249IjEuMCIgZW5jb2Rpbmc9IlVURi04IiBzdGFuZGFsb25lPSJ5ZXMiPz4KPFR5cGVzIHhtbG5zPSJodHRwOi8vc2NoZW1hcy5vcGVueG1sZm9ybWF0cy5vcmcvcGFja2FnZS8yMDA2L2NvbnRlbnQtdHlwZXMiPgogIDxEZWZhdWx0IEV4dGVuc2lvbj0icmVscyIgQ29udGVudFR5cGU9ImFwcGxpY2F0aW9uL3ZuZC5vcGVueG1sZm9ybWF0cy1wYWNrYWdlLnJlbGF0aW9uc2hpcHMreG1sIi8+CiAgPERlZmF1bHQgRXh0ZW5zaW9uPSJ4bWwiIENvbnRlbnRUeXBlPSJhcHBsaWNhdGlvbi94bWwiLz4KICA8T3ZlcnJpZGUgUGFydE5hbWU9Ii93b3JkL2RvY3VtZW50LnhtbCIgQ29udGVudFR5cGU9ImFwcGxpY2F0aW9uL3ZuZC5vcGVueG1sZm9ybWF0cy1vZmZpY2Vkb2N1bWVudC53b3JkcHJvY2Vzc2luZ21sLmRvY3VtZW50Lm1haW4reG1sIi8+CjwvVHlwZXM+UEsDBBQAAAAAADpZUl0gG4bqLgEAAC4BAAALAAAAX3JlbHMvLnJlbHM8P3htbCB2ZXJzaW9uPSIxLjAiIGVuY29kaW5nPSJVVEYtOCIgc3RhbmRhbG9uZT0ieWVzIj8+CjxSZWxhdGlvbnNoaXBzIHhtbG5zPSJodHRwOi8vc2NoZW1hcy5vcGVueG1sZm9ybWF0cy5vcmcvcGFja2FnZS8yMDA2L3JlbGF0aW9uc2hpcHMiPgogIDxSZWxhdGlvbnNoaXAgSWQ9InJJZDEiIFR5cGU9Imh0dHA6Ly9zY2hlbWFzLm9wZW54bWxmb3JtYXRzLm9yZy9vZmZpY2VEb2N1bWVudC8yMDA2L3JlbGF0aW9uc2hpcHMvb2ZmaWNlRG9jdW1lbnQiIFRhcmdldD0id29yZC9kb2N1bWVudC54bWwiLz4KPC9SZWxhdGlvbnNoaXBzPlBLAwQUAAAAAAA6WVJdZ5DGskUBAABFAQAAEQAAAHdvcmQvZG9jdW1lbnQueG1sPD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZz0iVVRGLTgiIHN0YW5kYWxvbmU9InllcyI/Pgo8dzpkb2N1bWVudCB4bWxuczp3PSJodHRwOi8vc2NoZW1hcy5vcGVueG1sZm9ybWF0cy5vcmcvd29yZHByb2Nlc3NpbmdtbC8yMDA2L21haW4iPgogIDx3OmJvZHk+CiAgICA8dzpwPjx3OnI+PHc6dD5IZWxsbyBhcnRpZmFjdDwvdzp0PjwvdzpyPjwvdzpwPgogICAgPHc6cD48dzpyPjx3OmJyIHc6dHlwZT0icGFnZSIvPjwvdzpyPjwvdzpwPgogICAgPHc6cD48dzpyPjx3OnQ+U2Vjb25kIHBhZ2U8L3c6dD48L3c6cj48L3c6cD4KICA8L3c6Ym9keT4KPC93OmRvY3VtZW50PlBLAQIUAxQAAAAAADpZUl3XeYTquAEAALgBAAATAAAAAAAAAAAAAACAAQAAAABbQ29udGVudF9UeXBlc10ueG1sUEsBAhQDFAAAAAAAOllSXSAbhuouAQAALgEAAAsAAAAAAAAAAAAAAIAB6QEAAF9yZWxzLy5yZWxzUEsBAhQDFAAAAAAAOllSXWeQxrJFAQAARQEAABEAAAAAAAAAAAAAAIABQAMAAHdvcmQvZG9jdW1lbnQueG1sUEsFBgAAAAADAAMAuQAAALQEAAAAAA==",
), (char) => char.charCodeAt(0));

describe("docx-preview layout", () => {
  it("renders page sections from the docx bytes and does not prefix a page count", async () => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    await renderAsync(sampleDocx, host);
    const pages = [...host.querySelectorAll("section.docx")];
    const text = pages.map((page) => page.textContent ?? "").join("\n");
    expect(pages.length).toBeGreaterThan(0);
    expect(text).toContain("Hello artifact");
    expect(text).toContain("Second page");
    expect(text.trim().startsWith("2")).toBe(false);
    host.remove();
  });
});

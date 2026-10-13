import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CodeEditor } from "@/components/code-editor";

describe("CodeEditor", () => {
  it("can show content read-only", () => {
    const { container } = render(<CodeEditor value={"services: {}\n"} filename="compose.yml" readOnly />);
    const content = container.querySelector(".cm-content");
    expect(content).toHaveTextContent("services: {}");
    expect(content).toHaveAttribute("contenteditable", "false");
  });

  it("is editable by default", () => {
    const { container } = render(<CodeEditor value="x" onChange={() => {}} />);
    expect(container.querySelector(".cm-content")).toHaveAttribute("contenteditable", "true");
  });
});

import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { FilesSection } from "@/components/files-section";
import type { AppFileMeta } from "@/types/app-config";

vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));
vi.mock("@/components/image-tag-picker", () => ({
  ImageTagPicker: ({ image, onSelect }: { image: string; onSelect: (ref: string) => void }) => (
    <button onClick={() => onSelect(`${image.split(":")[0]}:2`)}>tags of {image}</button>
  ),
}));

function Harness({ allowSecret, imageChips }: { allowSecret?: boolean; imageChips?: boolean }) {
  const [files, setFiles] = useState<AppFileMeta[]>([{ id: "f1", filename: "compose.yml", is_encrypted: false }]);
  const [contents, setContents] = useState<Record<string, string>>({ f1: "services:\n  app:\n    image: nginx:1\n" });
  return (
    <>
      <FilesSection
        files={files}
        contents={contents}
        onChange={(f, c) => {
          setFiles(f);
          setContents(c);
        }}
        description="the files"
        allowSecret={allowSecret}
        imageChips={imageChips}
      />
      <output aria-label="state">{JSON.stringify({ files, contents })}</output>
    </>
  );
}

const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("FilesSection", () => {
  it("adds, renames, marks secret, edits and removes files", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    const names = screen.getAllByRole("textbox", { name: "File name" });
    await user.type(names[1], "app.env");
    await user.click(screen.getAllByRole("button", { name: /Secret/ })[1]);
    await user.type(screen.getByRole("textbox", { name: "Content of app.env" }), "A=1");
    expect(state().files[1]).toMatchObject({ filename: "app.env", is_encrypted: true });
    expect(Object.values(state().contents)).toContain("A=1");
    await user.click(screen.getByRole("button", { name: "Remove app.env" }));
    expect(state().files).toHaveLength(1);
    expect(Object.keys(state().contents)).toEqual(["f1"]);
  });

  it("rewrites an image reference from the tag browser", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "tags of nginx:1" }));
    expect(state().contents.f1).toContain("image: nginx:2");
  });

  it("can leave out the secret toggle and the image browser", async () => {
    const user = userEvent.setup();
    render(<Harness allowSecret={false} imageChips={false} />);
    expect(screen.queryByRole("button", { name: /Secret/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /tags of/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove compose.yml" }));
    expect(screen.getByText(/Add at least a/)).toBeInTheDocument();
  });
});

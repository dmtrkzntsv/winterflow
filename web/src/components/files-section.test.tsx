import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { FileEditors, FilesSection } from "@/components/files-section";
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

function Harness({
  allowSecret,
  imageChips,
  errors,
  emptyNote,
}: {
  allowSecret?: boolean;
  imageChips?: boolean;
  errors?: Record<string, string>;
  emptyNote?: string;
}) {
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
        errors={errors}
        emptyNote={emptyNote}
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

  it("says its own empty note instead of asking for a compose file", async () => {
    const user = userEvent.setup();
    render(<Harness emptyNote="The repository's compose file runs." />);
    await user.click(screen.getByRole("button", { name: "Remove compose.yml" }));
    expect(screen.getByText("The repository's compose file runs.")).toBeInTheDocument();
    expect(screen.queryByText(/Add at least a/)).not.toBeInTheDocument();
  });
});

describe("FilesSection messages", () => {
  it("marks a file's name and says why the list is refused", () => {
    render(<Harness errors={{ "file:f1": "Name this file.", files: "Add a compose file named compose.yml." }} />);
    const name = screen.getByRole("textbox", { name: "File name" });
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription("Name this file.");
    expect(screen.getByText("Add a compose file named compose.yml.")).toBeInTheDocument();
  });
});

describe("FileEditors", () => {
  it("adds a file from its link and leaves out the image chips on request", async () => {
    const user = userEvent.setup();
    function Editors() {
      const [files, setFiles] = useState<AppFileMeta[]>([{ id: "f1", filename: "compose.yml", is_encrypted: false }]);
      const [contents, setContents] = useState<Record<string, string>>({ f1: "services:\n  app:\n    image: nginx:1\n" });
      return (
        <FileEditors
          files={files}
          contents={contents}
          onChange={(f, c) => {
            setFiles(f);
            setContents(c);
          }}
          imageChips={false}
          addLabel="Add another file"
        />
      );
    }
    render(<Editors />);
    expect(screen.queryByRole("button", { name: /tags of/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add another file" }));
    expect(screen.getAllByRole("textbox", { name: "File name" })).toHaveLength(2);
  });
});

describe("FileEditors: a stored secret file", () => {
  // StoredSecret is an app whose key.pem the agent holds (read back as the
  // placeholder).
  function StoredSecret({ errors }: { errors?: Record<string, string> }) {
    const [files, setFiles] = useState<AppFileMeta[]>([{ id: "k", filename: "key.pem", is_encrypted: true, storedAs: "key.pem" }]);
    const [contents, setContents] = useState<Record<string, string>>({ k: "<encrypted>" });
    return (
      <>
        <FileEditors
          files={files}
          contents={contents}
          onChange={(f, c) => {
            setFiles(f);
            setContents(c);
          }}
          errors={errors}
        />
        <output aria-label="state">{JSON.stringify({ files, contents })}</output>
      </>
    );
  }

  it("never shows its placeholder: Replace opens an empty editor, Keep current goes back", async () => {
    const user = userEvent.setup();
    render(<StoredSecret />);
    expect(screen.getByText("Secret · set")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Content of key.pem" })).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue("<encrypted>")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Replace key.pem" }));
    const editor = screen.getByRole("textbox", { name: "Content of key.pem" });
    expect(editor).toHaveValue("");
    expect(screen.getByText("Leave empty to keep the current contents.")).toBeInTheDocument();
    await user.type(editor, "KEY");
    expect(state().contents).toEqual({ k: "KEY" });

    await user.click(screen.getByRole("button", { name: "Keep current" }));
    expect(screen.getByText("Secret · set")).toBeInTheDocument();
    expect(state().contents).toEqual({ k: "<encrypted>" });
  });

  it("empties it when turned plain, so the placeholder never becomes text", async () => {
    const user = userEvent.setup();
    render(<StoredSecret />);
    await user.click(screen.getByRole("button", { name: /Secret$/ }));
    expect(screen.getByRole("textbox", { name: "Content of key.pem" })).toHaveValue("");
    expect(state().contents).toEqual({ k: "" });
    expect(state().files[0]).toMatchObject({ is_encrypted: false });
  });

  it("empties it when renamed: a renamed secret file needs its contents again", async () => {
    const user = userEvent.setup();
    render(<StoredSecret errors={{ "file:k:content": "Paste the contents again." }} />);
    await user.type(screen.getByRole("textbox", { name: "File name" }), "2");
    expect(state().files[0]).toMatchObject({ filename: "key.pem2", is_encrypted: true, storedAs: "key.pem" });
    expect(state().contents).toEqual({ k: "" });
    expect(screen.queryByText("Leave empty to keep the current contents.")).not.toBeInTheDocument();
    expect(screen.getByText("Paste the contents again.")).toBeInTheDocument();
    // Renamed back while still empty, it stands for the stored file again.
    await user.type(screen.getByRole("textbox", { name: "File name" }), "{Backspace}");
    expect(screen.getByText("Leave empty to keep the current contents.")).toBeInTheDocument();
  });
});

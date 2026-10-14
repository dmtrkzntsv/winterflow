import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ImageVersions } from "@/components/image-versions";
import type { AppFileMeta } from "@/types/app-config";

vi.mock("@/components/image-tag-picker", () => ({
  ImageTagPicker: ({ image, onSelect }: { image: string; onSelect: (ref: string) => void }) => (
    <button type="button" onClick={() => onSelect(`${image.split(":")[0]}:2`)}>
      tags of {image}
    </button>
  ),
}));

const file = (id: string, filename: string): AppFileMeta => ({ id, filename, is_encrypted: false }) as AppFileMeta;

describe("ImageVersions", () => {
  it("lists the images of each compose file that names one and rewrites the pick in place", async () => {
    const user = userEvent.setup();
    const onReplace = vi.fn();
    render(
      <ImageVersions
        files={[file("f1", "compose.yml"), file("f2", "override.yml"), file("f3", "notes.txt")]}
        contents={{
          f1: "services:\n  web:\n    image: nginx:1\n  db:\n    image: postgres:15\n",
          f2: "services: {}\n",
          f3: "image: ignored:1\n",
        }}
        onReplace={onReplace}
      />,
    );
    expect(screen.getByRole("heading", { name: "Image versions in compose.yml" })).toBeInTheDocument();
    // A compose file without images, and a file that isn't a compose file, add nothing.
    expect(screen.getAllByRole("heading")).toHaveLength(1);
    expect(screen.queryByRole("button", { name: /ignored/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "tags of postgres:15" }));
    expect(onReplace).toHaveBeenCalledWith("f1", "postgres:15", "postgres:2");
  });

  it("renders nothing when no compose file names an image", () => {
    const { container } = render(
      <ImageVersions files={[file("f1", "compose.yml")]} contents={{ f1: "services: {}\n" }} onReplace={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

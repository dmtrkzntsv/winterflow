import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ImageTagPicker } from "@/components/image-tag-picker";
import { stubViewport } from "@/test/media";

const getImageTags = vi.fn();
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ getImageTags }) }));

afterEach(() => {
  vi.unstubAllGlobals();
  getImageTags.mockReset();
});

describe("ImageTagPicker", () => {
  it("lists the image's tags, filters them and rewrites the reference with the picked one", async () => {
    const user = userEvent.setup({ delay: null });
    getImageTags.mockResolvedValue(["1.25", "1.26", "latest"]);
    const onSelect = vi.fn();
    render(<ImageTagPicker image="registry.example/nginx:1.25" onSelect={onSelect} />);
    await user.click(screen.getByRole("button", { name: "registry.example/nginx:1.25" }));
    const dialog = screen.getByRole("dialog", { name: "registry.example/nginx" });
    expect(dialog).toHaveTextContent("Pick a tag to use in the compose file.");
    await user.type(await screen.findByPlaceholderText("Filter tags…"), "1.2");
    expect(screen.queryByRole("button", { name: "latest" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "1.26" }));
    expect(onSelect).toHaveBeenCalledWith("registry.example/nginx:1.26");
    expect(getImageTags).toHaveBeenCalledWith("registry.example/nginx:1.25");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("hands focus back to its button when the dialog closes", async () => {
    const user = userEvent.setup({ delay: null });
    getImageTags.mockResolvedValue(["latest"]);
    render(<ImageTagPicker image="nginx:1.25" onSelect={vi.fn()} />);
    const button = screen.getByRole("button", { name: "nginx:1.25" });
    await user.click(button);
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(button).toHaveFocus();
  });

  it("says when no tag matches and when the tags can't be read", async () => {
    const user = userEvent.setup({ delay: null });
    getImageTags.mockResolvedValueOnce(["latest"]);
    const view = render(<ImageTagPicker image="redis" onSelect={() => {}} />);
    await user.click(screen.getByRole("button", { name: "redis" }));
    await user.type(await screen.findByPlaceholderText("Filter tags…"), "9");
    expect(screen.getByText("No matching tags.")).toBeInTheDocument();
    view.unmount();
    getImageTags.mockRejectedValueOnce(new Error("the registry did not answer"));
    render(<ImageTagPicker image="redis" onSelect={() => {}} />);
    await user.click(screen.getByRole("button", { name: "redis" }));
    expect(await screen.findByText("the registry did not answer")).toBeInTheDocument();
  });

  it("is a bottom sheet on a phone", async () => {
    stubViewport(375);
    const user = userEvent.setup({ delay: null });
    getImageTags.mockResolvedValue(["latest"]);
    render(<ImageTagPicker image="redis" onSelect={() => {}} />);
    await user.click(screen.getByRole("button", { name: "redis" }));
    expect(screen.getByRole("dialog", { name: "redis" })).toHaveClass("rounded-t-[10px]");
    expect(await screen.findByRole("button", { name: "latest" })).toBeInTheDocument();
  });
});

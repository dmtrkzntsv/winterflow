import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ListPlaceholder } from "@/components/list-placeholder";
import { RefreshButton } from "@/components/refresh-button";
import { RemoveButton } from "@/components/remove-button";
import { Row, RowList } from "@/components/section";

describe("list parts", () => {
  it("shows a spinner while loading, then the failure with Try again", async () => {
    const user = userEvent.setup({ delay: null });
    const retry = vi.fn();
    const { rerender } = render(<ListPlaceholder loading failed="Couldn't load domains." onRetry={retry} />);
    expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
    expect(screen.queryByText("Couldn't load domains.")).not.toBeInTheDocument();
    rerender(<ListPlaceholder loading={false} failed="Couldn't load domains." onRetry={retry} />);
    expect(screen.getByText("Couldn't load domains.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalled();
  });

  it("names the refresh and remove buttons and turns refresh off while loading", async () => {
    const user = userEvent.setup({ delay: null });
    const reload = vi.fn();
    const remove = vi.fn();
    const { rerender } = render(
      <>
        <RefreshButton label="Refresh domains" loading={false} onClick={reload} />
        <RemoveButton label="Remove registry ghcr.io" onClick={remove} />
      </>,
    );
    await user.click(screen.getByRole("button", { name: "Refresh domains" }));
    await user.click(screen.getByRole("button", { name: "Remove registry ghcr.io" }));
    expect(reload).toHaveBeenCalled();
    expect(remove).toHaveBeenCalled();
    rerender(<RefreshButton label="Refresh domains" loading onClick={reload} />);
    expect(screen.getByRole("button", { name: "Refresh domains" })).toBeDisabled();
  });

  it("puts a test id on a row", () => {
    render(
      <RowList>
        <Row title="Ops" testId="channel-row" />
      </RowList>,
    );
    expect(screen.getByTestId("channel-row")).toHaveTextContent("Ops");
  });
});

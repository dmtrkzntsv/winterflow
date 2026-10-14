import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { PageTitleProvider } from "@/layouts/page-title";
import { useCurrentPageTitle, usePageTitle } from "@/layouts/use-page-title";

function Page({ title }: { title: string }) {
  usePageTitle(title);
  return null;
}

function Bar() {
  return <p data-testid="bar">{useCurrentPageTitle()}</p>;
}

function Switcher() {
  const [page, setPage] = useState<"a" | "b" | "none">("a");
  return (
    <>
      {page === "a" ? <Page title="Apps" /> : page === "b" ? <Page title="Monitoring" /> : null}
      <button onClick={() => setPage("b")}>b</button>
      <button onClick={() => setPage("none")}>none</button>
    </>
  );
}

describe("usePageTitle", () => {
  afterEach(() => {
    document.title = "";
  });

  it("names the page in the shell and in the browser tab", async () => {
    render(
      <PageTitleProvider>
        <Page title="Members" />
        <Bar />
      </PageTitleProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("bar")).toHaveTextContent("Members"));
    expect(document.title).toBe("Members · Winterflow");
  });

  it("follows the page on navigation and clears when no page names itself", async () => {
    render(
      <PageTitleProvider>
        <Switcher />
        <Bar />
      </PageTitleProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("bar")).toHaveTextContent("Apps"));
    fireEvent.click(screen.getByRole("button", { name: "b" }));
    await waitFor(() => expect(screen.getByTestId("bar")).toHaveTextContent("Monitoring"));
    fireEvent.click(screen.getByRole("button", { name: "none" }));
    await waitFor(() => expect(screen.getByTestId("bar")).toBeEmptyDOMElement());
  });

  it("still names the browser tab outside the shell", () => {
    render(<Page title="Inbox" />);
    expect(document.title).toBe("Inbox · Winterflow");
  });
});

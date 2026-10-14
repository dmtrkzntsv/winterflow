import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { NotFound } from "@/components/not-found";
import { PageTitleProvider } from "@/layouts/page-title";
import { useCurrentPageTitle } from "@/layouts/use-page-title";

function Title() {
  return <span data-testid="title">{useCurrentPageTitle()}</span>;
}

describe("NotFound", () => {
  it("names the page and leads back to Apps", () => {
    render(
      <MemoryRouter>
        <PageTitleProvider>
          <NotFound />
          <Title />
        </PageTitleProvider>
      </MemoryRouter>,
    );
    expect(screen.getByTestId("title")).toHaveTextContent("Not found");
    expect(document.title).toBe("Not found · WinterFlow");
    expect(screen.getByRole("link", { name: "Go to Apps" })).toHaveAttribute("href", "/");
    expect(screen.queryByText(/dashboard/i)).not.toBeInTheDocument();
  });
});

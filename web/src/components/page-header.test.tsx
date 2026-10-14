import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";

describe("PageHeader", () => {
  afterEach(() => {
    document.title = "";
  });

  it("is the page's title, subtitle and actions", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Apps" subtitle="homelab · 3 running" actions={<Button>New app</Button>} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Apps" })).toHaveClass("text-2xl", "font-semibold");
    expect(screen.getByText("homelab · 3 running")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New app" })).toBeInTheDocument();
    expect(document.title).toBe("Apps · WinterFlow");
  });

  it("offers a back link on nested pages", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Website" back={{ to: "/templates", label: "Templates" }} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "Templates" })).toHaveAttribute("href", "/templates");
  });

  it("stacks the actions under the title on a phone", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Apps" actions={<Button>New app</Button>} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("banner")).toHaveClass("flex-col", "sm:flex-row");
  });
});

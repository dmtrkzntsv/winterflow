import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { Row, RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";

describe("Section", () => {
  it("is a region named by its 15px title, with actions beside it", () => {
    render(
      <Section title="Registries" description="Where images come from" actions={<Button>Add registry</Button>}>
        <p>body</p>
      </Section>,
    );
    const region = screen.getByRole("region", { name: "Registries" });
    expect(within(region).getByRole("heading", { level: 2, name: "Registries" })).toHaveClass("text-[15px]", "font-semibold");
    expect(within(region).getByText("Where images come from")).toBeInTheDocument();
    expect(within(region).getByRole("button", { name: "Add registry" })).toBeInTheDocument();
    expect(within(region).getByText("body")).toBeInTheDocument();
  });
});

describe("RowList", () => {
  it("lists flat rows that become stacked cards on a phone", () => {
    render(
      <MemoryRouter>
        <RowList label="Domains">
          <Row title="example.com" description="Automatic HTTPS" meta="2 apps" to="/x" actions={<Button>Edit</Button>} />
          <Row title="b.example.com" />
        </RowList>
      </MemoryRouter>,
    );
    const list = screen.getByRole("list", { name: "Domains" });
    expect(list).toHaveClass("divide-y", "max-sm:divide-y-0");
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveClass("flex-col", "sm:flex-row", "max-sm:rounded-xl");
    expect(within(rows[0]).getByRole("link", { name: "example.com" })).toHaveAttribute("href", "/x");
    expect(within(rows[0]).getByText("Automatic HTTPS")).toBeInTheDocument();
    expect(within(rows[0]).getByText("2 apps")).toBeInTheDocument();
    expect(within(rows[0]).getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(within(rows[1]).queryByRole("link")).not.toBeInTheDocument();
    // The link covers the row, so its keyboard focus rings the whole row.
    expect(within(rows[0]).getByRole("link")).toHaveClass("focus-visible:after:ring-2", "focus-visible:after:ring-ring");
    // Only a row that is a link answers the pointer.
    expect(rows[0]).toHaveClass("hover:bg-muted/40");
    expect(rows[1]).not.toHaveClass("hover:bg-muted/40");
  });
});

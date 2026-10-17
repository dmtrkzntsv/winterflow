import { fireEvent, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CatalogLogo } from "@/components/catalog-logo";

const template = {
  id: "c1",
  source: "catalog" as const,
  name: "Actual Budget",
  catalog: { logo_url: "https://winterflow.io/catalog/logos/actual-budget.png" },
};

describe("CatalogLogo", () => {
  it("loads the catalog URL directly, revealing the logo only after it loads", () => {
    const { container, rerender } = render(<CatalogLogo template={template} className="size-10" />);
    const img = container.querySelector("img")!;
    expect(img).toHaveAttribute("src", "https://winterflow.io/catalog/logos/actual-budget.png");
    expect(img).toHaveAttribute("referrerpolicy", "no-referrer");
    expect(img).toHaveClass("opacity-0");
    fireEvent.load(img);
    expect(img).toHaveClass("opacity-100");
    rerender(<CatalogLogo template={{ ...template, catalog: { logo_url: "https://winterflow.io/catalog/logos/new.png" } }} />);
    expect(container.querySelector("img")).toHaveClass("opacity-0");
  });

  it("keeps the fallback tile when an image fails or has no logo", () => {
    const { container, rerender } = render(<CatalogLogo template={template} />);
    fireEvent.error(container.querySelector("img")!);
    expect(container.querySelector("img")).toBeNull();
    expect(container).toHaveTextContent("A");
    rerender(<CatalogLogo template={{ ...template, catalog: { logo_url: "" } }} />);
    expect(container.querySelector("img")).toBeNull();
  });
});

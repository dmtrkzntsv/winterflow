import { render, screen } from "@testing-library/react";
import { Rocket } from "lucide-react";
import { describe, expect, it } from "vitest";

import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";

describe("EmptyState", () => {
  it("says one sentence and offers one action", () => {
    render(<EmptyState icon={Rocket} title="Run your first app." action={<Button>New app</Button>} />);
    expect(screen.getByText("Run your first app.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New app" })).toBeInTheDocument();
  });

  it("can be just the sentence", () => {
    render(<EmptyState title="All quiet." />);
    expect(screen.getByText("All quiet.")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});

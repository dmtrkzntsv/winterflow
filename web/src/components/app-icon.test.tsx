import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AppIcon } from "@/components/app-icon";

describe("AppIcon", () => {
  it("shows the name's initial", () => {
    expect(render(<AppIcon name="nextcloud" />).container).toHaveTextContent("N");
  });

  it("shows a generic box, never a character of the id, for an app without a name", () => {
    for (const name of [undefined, "", "   "]) {
      const { container, unmount } = render(<AppIcon name={name} />);
      expect(container).toHaveTextContent(/^$/);
      expect(container.querySelector("svg")).toBeInTheDocument();
      unmount();
    }
  });

  it("shows the chosen icon over the initial", () => {
    const { container } = render(<AppIcon name="nextcloud" icon="Box" />);
    expect(container.querySelector("svg")).toBeInTheDocument();
    expect(container).not.toHaveTextContent("N");
  });
});

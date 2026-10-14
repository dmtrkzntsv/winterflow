import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ParameterFields } from "@/components/parameter-fields";

describe("ParameterFields", () => {
  it("labels a field by the parameter's description, its name small beside it, and tags a secret", () => {
    render(
      <ParameterFields
        params={[
          { name: "PORT", required: true, description: "Host port the app is reachable on" },
          { name: "REDIS_PASSWORD", secret: true },
          { name: "TITLE" },
        ]}
        values={{}}
        onChange={vi.fn()}
      />,
    );
    const port = screen.getByLabelText("Host port the app is reachable on (PORT)");
    const label = document.querySelector(`label[for="${port.id}"]`)!;
    expect(label).toHaveTextContent("Host port the app is reachable on*PORT");
    expect(label.querySelector("code")).toHaveTextContent("PORT");
    const secret = document.querySelector(`label[for="${screen.getByLabelText("REDIS_PASSWORD").id}"]`)!;
    expect(secret).toHaveTextContent("REDIS_PASSWORDSecret");
    expect(secret.querySelector("code")).toBeNull();
    expect(secret).not.toHaveTextContent("*secret");
    expect(screen.getByLabelText("TITLE")).toBeInTheDocument();
  });
});

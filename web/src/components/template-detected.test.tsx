import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TemplateDetected } from "@/components/template-detected";
import type { Detected } from "@/lib/templates";

const DETECTED: Detected = {
  variables: [
    { name: "BASE_URL", required: true, encrypted: false, pattern: "^https?://", description: "Public URL", default: "http://localhost", services: ["app"] },
    { name: "SECRET", required: true, encrypted: true, default: null, services: ["app"] },
    { name: "EMPTY", required: false, encrypted: false, default: "", services: ["app"] },
  ],
  expose: [
    { id: "app:8080/http", target: "app:8080", container: "app", port: 8080, protocol: "http" },
    { id: "db:5432/tcp", target: "db:5432", container: "db", port: 5432, protocol: "tcp" },
  ],
  backups: [{ name: "db", type: "files" }],
  warnings: ["x-winterflow.variables.UNUSED: no service environment and no override uses it"],
};

describe("TemplateDetected", () => {
  it("lists the variables, exposed ports, backups and warnings the compose file declares", () => {
    render(<TemplateDetected detected={DETECTED} />);
    const vars = within(screen.getByRole("region", { name: "Variables" }));
    expect(vars.getByText("BASE_URL")).toBeInTheDocument();
    expect(vars.getByText("default http://localhost")).toBeInTheDocument();
    expect(vars.getByText("Public URL")).toBeInTheDocument();
    expect(vars.getByText("^https?://")).toBeInTheDocument();
    expect(vars.getAllByText("Required")).toHaveLength(2);
    expect(vars.getByText("Secret")).toBeInTheDocument();
    expect(vars.getByText('default ""')).toBeInTheDocument();
    const ports = within(screen.getByRole("region", { name: "Exposed ports" }));
    expect(ports.getByText("takes hostnames")).toBeInTheDocument();
    expect(ports.getByText("reachable on the LAN")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Backups" })).getByText("files")).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Warnings" })).toHaveTextContent("UNUSED");
  });

  it("says when nothing is declared, and shows a problem, a read error and a check in progress", () => {
    const { rerender } = render(<TemplateDetected detected={{ variables: null, expose: null, backups: null, warnings: null }} />);
    expect(screen.getByText(/No variables, exposed ports or backups/)).toBeInTheDocument();
    rerender(<TemplateDetected detected={null} problem="files: a compose file is required" error="offline" checking />);
    const alerts = screen.getAllByRole("alert");
    expect(alerts[0]).toHaveTextContent("A save would be refused: files: a compose file is required");
    expect(alerts[1]).toHaveTextContent("Couldn't read the files: offline");
    expect(screen.getByText("Checking…")).toBeInTheDocument();
    expect(screen.queryByText(/No variables/)).not.toBeInTheDocument();
  });
});

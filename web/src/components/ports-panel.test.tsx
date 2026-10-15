import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { PortsPanel } from "@/components/ports-panel";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, ExposedPort } from "@/types/app-config";

const EXPOSE: ExposedPort[] = [
  { id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false },
  { id: "dns:53/udp", container: "dns", port: 53, protocol: "udp", host_port: 5353, bind: "0.0.0.0", pinned: true },
];

function Harness({ errors, serverIp = "192.168.1.10" }: { errors?: FieldErrors; serverIp?: string }) {
  const [state, setState] = useState<AppEditorState>({
    ...emptyEditorState(),
    expose: EXPOSE,
    config: { ...emptyEditorState().config, ports: { "app:8080/http": { host: 20013, pinned: false }, "dns:53/udp": { host: 5353, pinned: true } } },
  });
  return (
    <>
      <PortsPanel state={state} onChange={setState} serverIp={serverIp} errors={errors} />
      <output aria-label="ports">{JSON.stringify(state.config.ports)}</output>
    </>
  );
}
const ports = () => JSON.parse(screen.getByRole("status", { name: "ports" }).textContent ?? "{}");

describe("PortsPanel", () => {
  it("shows each entry's port, where it listens, and a raw entry's address to copy", () => {
    render(<Harness />);
    const rows = screen.getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("app:8080");
    expect(rows[0]).toHaveTextContent("localhost");
    expect(rows[0]).toHaveTextContent("port 20013");
    expect(rows[1]).toHaveTextContent("LAN");
    expect(screen.getByLabelText("Address of dns:53/udp")).toHaveValue("192.168.1.10:5353");
    expect(screen.queryByLabelText("Address of app:8080/http")).not.toBeInTheDocument();
  });

  it("pins a port at its current number, edits and unpins it", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("switch", { name: "Pin port of app:8080/http" }));
    expect(ports()["app:8080/http"]).toEqual({ host: 20013, pinned: true });
    const input = screen.getByLabelText("Pinned port of app:8080/http");
    await user.clear(input);
    await user.type(input, "8080");
    expect(ports()["app:8080/http"]).toEqual({ host: 8080, pinned: true });
    await user.click(screen.getByRole("switch", { name: "Pin port of dns:53/udp" }));
    expect(ports()["dns:53/udp"]).toEqual({ host: 5353, pinned: false });
  });

  it("shows a pin's error and nothing without expose entries", () => {
    const { unmount } = render(<Harness errors={{ "pin:dns:53/udp": "Enter a port from 1 to 65535." }} />);
    expect(screen.getByText("Enter a port from 1 to 65535.")).toBeInTheDocument();
    unmount();
    render(<PortsPanel state={emptyEditorState()} onChange={() => {}} />);
    expect(screen.queryByRole("list", { name: "Ports" })).not.toBeInTheDocument();
  });

  it("keeps the pin control a phone-sized tap target", () => {
    render(<Harness />);
    const label = screen.getByRole("switch", { name: "Pin port of app:8080/http" }).closest("label");
    expect(label).toHaveClass("max-sm:min-h-10", "items-center");
  });
});

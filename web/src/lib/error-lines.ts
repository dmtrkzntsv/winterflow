// lastLine is an error's last non-empty line: with docker and compose
// output, the line that says what went wrong.
export function lastLine(text: string): string {
  const lines = text.split("\n").map((l) => l.trim()).filter(Boolean);
  return lines.at(-1) ?? "";
}

// A failed deploy's error starts with how compose was run ("docker compose
// up: exit status 1: "): the reason is what follows.
const COMPOSE_PREFIX = /^(?:docker compose [a-z]+: )?(?:exit status \d+: )?/;

// errorHeadline is the line of an agent or docker error that says what went
// wrong: its last non-empty line (compose prints progress first), trimmed,
// without the compose-invocation prefix.
export function errorHeadline(text: string): string {
  const lines = text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const last = lines.at(-1) ?? "";
  return last.replace(COMPOSE_PREFIX, "") || last;
}

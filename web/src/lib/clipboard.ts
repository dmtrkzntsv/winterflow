// copyText copies text, also on a plain-http LAN origin where browsers
// hide navigator.clipboard (the constraint crypto.subtle has in lib/ecies):
// it falls back to the selection-based copy and rejects when that fails.
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    if (!document.execCommand("copy")) throw new Error("copy rejected");
  } finally {
    area.remove();
  }
}

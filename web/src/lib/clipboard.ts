// copyText copies text, also on a plain-http LAN origin where browsers
// hide navigator.clipboard (the constraint crypto.subtle has in lib/ecies):
// it falls back to the selection-based copy and rejects when that fails.
// The helper field goes inside the dialog that has focus, if any: a modal
// dialog keeps focus (and selection) from leaving it for the page behind.
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  const host = document.activeElement?.closest('[role="dialog"], [role="alertdialog"]') ?? document.body;
  host.appendChild(area);
  area.select();
  try {
    if (!document.execCommand("copy")) throw new Error("copy rejected");
  } finally {
    area.remove();
  }
}

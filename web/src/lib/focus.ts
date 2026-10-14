import type { RefObject } from "react";

// returnFocusTo is an onCloseAutoFocus handler for a dialog without a
// trigger of its own (one that remounts per opening, or opened from a table
// row): focus goes back to the first of targets still on the page, instead
// of falling to the page itself.
export function returnFocusTo(...targets: (RefObject<HTMLElement | null> | undefined)[]) {
  return (e: Event) => {
    const back = targets.map((t) => t?.current).find((el) => el?.isConnected);
    if (back) {
      e.preventDefault();
      back.focus();
    }
  };
}

const FOCUSABLE = 'input:not([type="hidden"]), select, textarea, button, a[href], [tabindex]';

// focusFirstField focuses the first focusable element inside container
// that isn't a dialog's close button ([data-dialog-close]) and isn't
// skipped by Tab (tabindex -1, e.g. a radio group's unchecked choices).
// False when there is none.
export function focusFirstField(container: HTMLElement | null): boolean {
  if (!container) return false;
  for (const el of container.querySelectorAll<HTMLElement>(FOCUSABLE)) {
    if (el === container || el.closest("[data-dialog-close]")) continue;
    if (el.getAttribute("tabindex") === "-1" || el.matches(":disabled") || el.closest("[aria-hidden='true'], [hidden]")) continue;
    el.focus();
    if (document.activeElement === el) return true;
  }
  return false;
}

// focusHeading focuses container's first heading (made focusable for it),
// for a panel whose fields aren't the point (a sheet of history). False
// when it has none.
export function focusHeading(container: HTMLElement | null): boolean {
  const heading = container?.querySelector<HTMLElement>("h1, h2, h3");
  if (!heading) return false;
  if (!heading.hasAttribute("tabindex")) heading.setAttribute("tabindex", "-1");
  heading.focus();
  return document.activeElement === heading;
}

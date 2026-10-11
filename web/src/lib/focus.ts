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

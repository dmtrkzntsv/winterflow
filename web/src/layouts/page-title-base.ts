import { createContext } from "react";

// The product name the browser tab shows after the page's title.
export const APP_NAME = "WinterFlow";

// PageTitleContext carries the current page's title (the mobile top bar
// shows it); SetPageTitleContext the setter pages call through
// usePageTitle. Both are absent outside the app shell.
export const PageTitleContext = createContext<string>("");
export const SetPageTitleContext = createContext<((title: string) => void) | null>(null);

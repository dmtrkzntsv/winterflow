import type { AppInstance } from "@/hooks/use-app-instance";

// Ownership is the template side of an app a template manages, with only
// this app's own instance; null for a plain app. While the instance is
// read (or when the read failed) the app is still the template's.
export type Ownership = AppInstance | null;

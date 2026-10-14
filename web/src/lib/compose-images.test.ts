import { describe, expect, it } from "vitest";

import { imageRefs } from "@/lib/compose-images";

describe("imageRefs", () => {
  it("lists each image a compose file names, once", () => {
    const content = `services:
  web:
    image: nginx:alpine
  db:
    image: "postgres:16"
  again:
    image: nginx:alpine
  # image: commented:1
`;
    // A commented-out line doesn't start with whitespace then image:.
    expect(imageRefs(content)).toEqual(["nginx:alpine", "postgres:16"]);
    expect(imageRefs("services: {}\n")).toEqual([]);
  });
});

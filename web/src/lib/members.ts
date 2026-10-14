// initials is a member's avatar text: the first letters of the first two
// words of the name, or of the email when there is no name; "?" for
// neither.
export function initials(name: string, email: string): string {
  const words = (name.trim() || email).split(/[\s@._-]+/).filter(Boolean);
  return (
    words
      .slice(0, 2)
      .map((w) => w.charAt(0).toUpperCase())
      .join("") || "?"
  );
}

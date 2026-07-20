import { useEffect, useState } from "react";
import { Tags } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { useApps } from "@/context/use-apps";

type Props = {
  image: string; // full ref as written in the compose file (may carry a tag)
  onSelect: (newRef: string) => void;
};

// ImageTagPicker shows the registry tags available for one image and rewrites
// the reference with the chosen tag.
export function ImageTagPicker({ image, onSelect }: Props) {
  const { getImageTags } = useApps();
  const [open, setOpen] = useState(false);
  const [tags, setTags] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  const baseRef = imageWithoutTag(image);

  useEffect(() => {
    if (!open || tags !== null) return;
    let cancelled = false;
    getImageTags(image)
      .then((t) => {
        if (!cancelled) setTags(t);
      })
      .catch((e) => {
        if (!cancelled)
          setError(e instanceof Error ? e.message : "Failed to load tags");
      });
    return () => {
      cancelled = true;
    };
  }, [open, tags, image, getImageTags]);

  const visible = (tags ?? []).filter((t) => t.includes(filter));

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline" className="h-7 gap-1 font-mono text-xs">
          <Tags className="size-3.5" />
          {image}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="font-mono text-base">{baseRef}</DialogTitle>
          <DialogDescription>
            Pick a tag to use in the compose file.
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : tags === null ? (
          <div className="flex h-32 items-center justify-center">
            <Spinner />
          </div>
        ) : (
          <div className="space-y-2">
            <Input
              placeholder="Filter tags…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <div className="max-h-64 space-y-1 overflow-y-auto">
              {visible.length === 0 ? (
                <p className="py-4 text-center text-sm text-muted-foreground">
                  No matching tags.
                </p>
              ) : (
                visible.map((tag) => (
                  <button
                    key={tag}
                    type="button"
                    className="block w-full rounded px-2 py-1.5 text-left font-mono text-sm hover:bg-muted"
                    onClick={() => {
                      onSelect(`${baseRef}:${tag}`);
                      setOpen(false);
                    }}
                  >
                    {tag}
                  </button>
                ))
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// imageWithoutTag strips the :tag suffix (digests are left alone — picking a
// tag replaces the whole reference anyway).
function imageWithoutTag(image: string): string {
  const slash = image.lastIndexOf("/");
  const colon = image.lastIndexOf(":");
  if (colon > slash) return image.slice(0, colon);
  return image;
}

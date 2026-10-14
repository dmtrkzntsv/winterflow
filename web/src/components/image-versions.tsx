import { ImageChips } from "@/components/files-section";
import { imageRefs } from "@/lib/compose-images";
import { isComposeFile, type AppFileMeta } from "@/types/app-config";

// ImageVersions is the "Image versions" block of the editors: for each
// compose file that names images, a tag browser per image that rewrites
// the reference in place. Nothing when no compose file names one.
export function ImageVersions({
  files,
  contents,
  onReplace,
}: {
  files: AppFileMeta[];
  contents: Record<string, string>;
  onReplace: (fileId: string, oldRef: string, newRef: string) => void;
}) {
  const composeFiles = files.filter((f) => isComposeFile(f.filename) && imageRefs(contents[f.id] ?? "").length > 0);
  return (
    <>
      {composeFiles.map((f) => (
        <div key={f.id} className="space-y-1">
          <h3 className="text-sm font-medium">
            Image versions in <code className="font-mono">{f.filename}</code>
          </h3>
          <div className="overflow-hidden rounded-md border">
            <ImageChips content={contents[f.id] ?? ""} onReplace={(oldRef, newRef) => onReplace(f.id, oldRef, newRef)} />
          </div>
        </div>
      ))}
    </>
  );
}

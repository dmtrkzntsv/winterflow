import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

// ListPlaceholder holds a list's place until its first answer: a spinner
// while loading, then what went wrong and (onRetry) a way to try again.
export function ListPlaceholder({
  loading,
  failed,
  onRetry,
}: {
  loading: boolean;
  failed: string;
  onRetry?: () => void;
}) {
  return (
    <div className="flex min-h-24 flex-col items-center justify-center gap-2 rounded-xl border border-dashed p-4 text-center text-sm text-muted-foreground">
      {loading ? (
        <Spinner />
      ) : (
        <>
          <p>{failed}</p>
          {onRetry ? (
            <Button size="sm" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          ) : null}
        </>
      )}
    </div>
  );
}

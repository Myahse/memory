import type { DuplicateError } from "../lib/upload";
import { MemoryCard } from "./MemoryCard";

export function DuplicateDialog({
  error,
  onChoose,
  onCancel,
}: {
  error: DuplicateError;
  onChoose: (choice: "keep_both" | "replace", replaceId?: string) => void;
  onCancel: () => void;
}) {
  const existing = error.duplicates[0];
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div role="alertdialog" aria-modal="true" className="card w-full max-w-md space-y-4 p-5">
        <h2 className="text-lg font-semibold">This looks similar to an existing memory.</h2>
        {existing && <MemoryCard memory={existing} compact />}
        <p className="text-sm text-zinc-500">Nothing is deleted unless you choose Replace.</p>
        <div className="grid grid-cols-3 gap-2">
          <button className="btn-ghost" onClick={onCancel}>
            Cancel
          </button>
          <button className="btn-ghost border border-zinc-300 dark:border-zinc-700" onClick={() => onChoose("replace", existing?.id)}>
            Replace
          </button>
          <button className="btn-primary" onClick={() => onChoose("keep_both")}>
            Keep both
          </button>
        </div>
      </div>
    </div>
  );
}

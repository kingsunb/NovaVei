import { LayoutGrid, LayoutList } from "lucide-react";
import { cn } from "@/lib/utils";

export type ViewMode = "grid" | "list";

interface ViewToggleProps {
  value: ViewMode;
  onChange: (value: ViewMode) => void;
  /** 当 list 模式的语义不是"列表"而是"表格"时使用 */
  listLabel?: string;
  gridLabel?: string;
}

/**
 * ViewToggle — 网格/列表视图切换按钮组。
 * 两个图标按钮并排放在圆角容器中，当前模式高亮。
 */
export function ViewToggle({
  value,
  onChange,
  listLabel = "列表",
  gridLabel = "网格",
}: ViewToggleProps) {
  return (
    <div className="flex items-center rounded-control border border-border/60 bg-card/70 p-0.5 backdrop-blur-sm">
      <button
        type="button"
        aria-label={gridLabel}
        title={gridLabel}
        aria-pressed={value === "grid"}
        onClick={() => onChange("grid")}
        className={cn(
          "flex h-7 w-8 items-center justify-center rounded-[6px] transition-colors",
          value === "grid"
            ? "bg-primary/[0.12] text-primary-text shadow-apple-sm"
            : "text-ink-muted hover:text-ink",
        )}
      >
        <LayoutGrid className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        aria-label={listLabel}
        title={listLabel}
        aria-pressed={value === "list"}
        onClick={() => onChange("list")}
        className={cn(
          "flex h-7 w-8 items-center justify-center rounded-[6px] transition-colors",
          value === "list"
            ? "bg-primary/[0.12] text-primary-text shadow-apple-sm"
            : "text-ink-muted hover:text-ink",
        )}
      >
        <LayoutList className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

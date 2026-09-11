import { NavLink } from "react-router-dom";
import {
  Activity,
  Bot,
  ChevronsLeft,
  ChevronsRight,
  GaugeCircle,
  KeyRound,
  LayoutGrid,
  Settings as SettingsIcon,
  ShieldCheck,
  UsersRound,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useSidebar } from "./useSidebar";

interface NavItem {
  to: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
}

const OPERATIONS: NavItem[] = [
  { to: "/dashboard", label: "总览", icon: GaugeCircle },
  { to: "/channels", label: "渠道", icon: LayoutGrid },
  { to: "/custom-models", label: "自定义模型", icon: Bot },
  { to: "/groups", label: "分组", icon: UsersRound },
  { to: "/mask", label: "脱敏", icon: ShieldCheck },
];

const ACCESS: NavItem[] = [
  { to: "/keys", label: "API 密钥", icon: KeyRound },
  { to: "/logs", label: "日志", icon: Activity },
  { to: "/settings", label: "设置", icon: SettingsIcon },
];

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { collapsed, toggle } = useSidebar();

  return (
    <aside
      id="primary-sidebar"
      data-testid="sidebar"
      data-collapsed={collapsed}
      aria-label="主导航"
      className={cn(
        "glass-sidebar group/sidebar relative z-30 flex h-full shrink-0 flex-col transition-[width] duration-200 ease-[cubic-bezier(0.25,0,0,1)]",
        collapsed ? "w-14" : "w-60",
      )}
    >
      {/* 品牌 */}
      <div className="flex h-14 items-center gap-2.5 px-4">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[10px] bg-gradient-to-br from-[#007AFF] to-[#5856D6] text-sm font-bold text-white shadow-apple-sm">
          N
        </div>
        <span
          className={cn(
            "truncate text-[15px] font-semibold tracking-tight text-ink",
            collapsed && "hidden",
          )}
        >
          NovaVei
        </span>
      </div>

      <nav className="flex-1 space-y-4 overflow-y-auto px-3 py-2" aria-label="导航">
        <NavGroup label="运营" items={OPERATIONS} collapsed={collapsed} onNavigate={onNavigate} />
        <NavGroup label="接入" items={ACCESS} collapsed={collapsed} onNavigate={onNavigate} />
      </nav>

      {/* 折叠按钮 */}
      <div className="border-t border-border/40 p-3">
        <button
          type="button"
          onClick={toggle}
          aria-label={collapsed ? "展开侧栏" : "折叠侧栏"}
          aria-pressed={collapsed}
          aria-controls="primary-sidebar"
          data-testid="collapse-btn"
          className={cn(
            // 44px 高：WCAG 2.5.5 交互目标下限
            "flex h-11 w-full items-center gap-2.5 rounded-control px-2 text-[13px] transition-all duration-150",
            "text-ink-subtle hover:bg-ink/[0.04] hover:text-ink-muted active:scale-[0.97]",
          )}
        >
          {collapsed ? (
            <ChevronsRight className="h-4 w-4 shrink-0" aria-hidden />
          ) : (
            <ChevronsLeft className="h-4 w-4 shrink-0" aria-hidden />
          )}
          <span className={cn(collapsed && "hidden")}>折叠</span>
        </button>
      </div>
    </aside>
  );
}

function NavGroup({
  label,
  items,
  collapsed,
  onNavigate,
}: {
  label: string;
  items: NavItem[];
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  return (
    <div className="space-y-0.5">
      <p
        className={cn(
          "px-2 pb-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-ink-subtle",
          collapsed && "sr-only",
        )}
      >
        {label}
      </p>
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          title={collapsed ? item.label : undefined}
          // 折叠态下文字 span 被 hidden，accessible name 必须由 aria-label 兜底，
          // 否则读屏用户丢失整个主导航（审计 4.17）。
          aria-label={item.label}
          onClick={onNavigate}
          className={({ isActive }) =>
            cn(
              "flex min-h-10 items-center gap-2.5 rounded-control px-2 text-[13px] font-medium tracking-tight transition-all duration-150",
              "active:scale-[0.97]",
              isActive
                ? "bg-primary/[0.08] text-primary-text"
                : "text-ink-muted hover:bg-ink/[0.04] hover:text-ink",
            )
          }
        >
          <item.icon className="h-4 w-4 shrink-0" aria-hidden />
          <span className={cn("truncate", collapsed && "hidden")}>
            {item.label}
          </span>
        </NavLink>
      ))}
    </div>
  );
}

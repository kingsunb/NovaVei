import { useLocation } from "react-router-dom";
import { Menu, Moon, Sun, LogOut } from "lucide-react";
import { useTheme } from "./ThemeProvider";
import { useAuth } from "@/store/auth";
import { Button } from "@/components/ui/button";

const TITLES: Record<string, string> = {
  "/dashboard": "总览",
  "/channels": "渠道",
  "/custom-models": "自定义模型",
  "/groups": "分组",
  "/keys": "API 密钥",
  "/logs": "日志",
  "/settings": "设置",
};

interface TopbarProps {
  onOpenCommand: () => void;
  onOpenNavigation: () => void;
}

export function Topbar({ onOpenNavigation }: TopbarProps) {
  const { resolved, toggle } = useTheme();
  const { username, logout } = useAuth();
  const { pathname } = useLocation();
  const title = TITLES[pathname] ?? "NovaVeil";

  return (
    <header className="glass-topbar sticky top-0 z-20 flex h-14 items-center gap-2 px-3 sm:gap-3 sm:px-6">
      <Button
        variant="ghost"
        size="icon"
        aria-label="打开主导航"
        title="打开主导航"
        onClick={onOpenNavigation}
        className="h-11 w-11 rounded-control md:hidden"
      >
        <Menu className="h-4 w-4" aria-hidden />
      </Button>
      <h2 className="truncate text-[15px] font-semibold tracking-tight text-ink">{title}</h2>

      <div className="ml-auto flex items-center gap-2">
        <Button
          variant="ghost"
          size="icon"
          onClick={toggle}
          aria-label="切换主题"
          title="切换主题"
          className="h-11 w-11 rounded-full"
        >
          {resolved === "dark" ? (
            <Sun className="h-4 w-4" aria-hidden />
          ) : (
            <Moon className="h-4 w-4" aria-hidden />
          )}
        </Button>

        <div className="mx-1 h-5 w-px bg-border/60" aria-hidden />

        <Button
          variant="ghost"
          size="sm"
          onClick={logout}
          title="退出登录"
          className="gap-2 rounded-full px-2"
        >
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-gradient-to-br from-[#007AFF] to-[#5856D6] text-[11px] font-bold text-white">
            {(username ?? "A")[0]?.toUpperCase()}
          </span>
          <span className="text-xs font-medium">{username ?? "admin"}</span>
          <LogOut className="h-3.5 w-3.5 text-ink-subtle" aria-hidden />
        </Button>
      </div>
    </header>
  );
}

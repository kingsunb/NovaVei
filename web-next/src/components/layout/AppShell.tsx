import { useEffect, useRef, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { Menu } from "lucide-react";
import { Sidebar } from "./Sidebar";
import { Topbar } from "./Topbar";
import { CommandPalette } from "./CommandPalette";
import { Button } from "@/components/ui/button";

/**
 * 应用壳：磨砂玻璃控制台布局。
 * 桌面使用常驻导航，小屏切换为可关闭的抽屉，保证主区始终有完整阅读宽度。
 */
export function AppShell({ children }: { children: ReactNode }) {
  const [cmdkOpen, setCmdkOpen] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  // 路由滚动复位（§3.2）：主滚动容器是 <main>，pathname 变化时即时回到顶部，
  // 避免新页面继承上一页面的滚动位置。用即时复位而非 smooth，防止切换时出现
  // 误解性的缓慢滚动；如后续需要按路由保存/恢复列表位置，再单独设计，不在此混用。
  const mainRef = useRef<HTMLElement>(null);
  const { pathname } = useLocation();
  useEffect(() => {
    const el = mainRef.current;
    if (el) el.scrollTop = 0;
  }, [pathname]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCmdkOpen((o) => !o);
      }
      if (e.key === "Escape") setMobileNavOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    document.body.style.overflow = mobileNavOpen ? "hidden" : "";
    return () => {
      document.body.style.overflow = "";
    };
  }, [mobileNavOpen]);

  return (
    <div className="bg-gradient-subtle flex h-full min-h-0 bg-background text-foreground">
      <div
        aria-hidden={!mobileNavOpen}
        className={mobileNavOpen ? "fixed inset-0 z-40 bg-ink/35 backdrop-blur-[2px] md:hidden" : "hidden"}
        onClick={() => setMobileNavOpen(false)}
      />
      <div
        className={
          mobileNavOpen
            ? "fixed inset-y-0 left-0 z-50 w-[min(84vw,18rem)] md:static md:w-auto"
            : "hidden md:block"
        }
      >
        <Sidebar onNavigate={() => setMobileNavOpen(false)} />
      </div>
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar
          onOpenCommand={() => setCmdkOpen(true)}
          onOpenNavigation={() => setMobileNavOpen(true)}
        />
        <main
          ref={mainRef}
          className="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-6 sm:py-6 lg:px-8"
        >
          <div className="mx-auto h-full w-full max-w-[1440px]">{children}</div>
        </main>
      </div>
      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} />
    </div>
  );
}

export function MobileMenuButton({ onClick }: { onClick: () => void }) {
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label="打开主导航"
      title="打开主导航"
      onClick={onClick}
      className="h-11 w-11 rounded-control md:hidden"
    >
      <Menu className="h-4 w-4" aria-hidden />
    </Button>
  );
}

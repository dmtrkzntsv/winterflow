import { useEffect, useRef, type ReactNode } from "react";

import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

const triggerClass =
  "-mb-px min-h-10 rounded-none border-b-2 border-transparent px-0.5 pb-2.5 pt-1 text-muted-foreground shadow-none transition-colors duration-150 hover:text-foreground sm:px-1 data-[state=active]:border-foreground data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none";

export type UnderlineTabsProps<T extends string> = {
  value: T;
  onValueChange: (value: T) => void;
  items: { value: T; label: ReactNode }[];
  // children are the panels (TabsContent from components/ui/tabs).
  children: ReactNode;
  // label names the tab list for assistive tech.
  label?: string;
  className?: string;
};

// UnderlineTabs is the app's one tab style: an underline bar that scrolls
// inside itself on a phone (and brings the selected tab into view), over
// the caller's panels. Pair it with useTabParam to keep the tab in ?tab=.
export function UnderlineTabs<T extends string>({
  value,
  onValueChange,
  items,
  children,
  label,
  className,
}: UnderlineTabsProps<T>) {
  const bar = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    bar.current
      ?.querySelector<HTMLElement>('[role="tab"][data-state="active"]')
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [value]);

  return (
    <Tabs value={value} onValueChange={(next) => onValueChange(next as T)} className={className}>
      <div ref={bar} className="-mx-1 max-w-full overflow-x-auto px-1">
        <TabsList
          aria-label={label}
          className="h-auto w-max min-w-full justify-start gap-6 rounded-none border-b bg-transparent p-0"
        >
          {items.map((item) => (
            <TabsTrigger key={item.value} value={item.value} className={triggerClass}>
              {item.label}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      {children}
    </Tabs>
  );
}

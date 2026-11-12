import type { LucideIcon } from "lucide-react";
import { useState } from "react";

export interface RibbonAction {
  id: string;
  label: string;
  Icon: LucideIcon;
  color?: string;
  onClick: () => void;
  disabled?: boolean;
  large?: boolean;
  active?: boolean;
}

export interface RibbonGroup {
  label: string;
  actions: RibbonAction[];
}

export interface RibbonTab {
  id: string;
  label: string;
  groups: RibbonGroup[];
}

export function Ribbon({ tabs }: { tabs: RibbonTab[] }) {
  const [current, setCurrent] = useState(tabs[0]?.id);
  const tab = tabs.find((t) => t.id === current) ?? tabs[0];
  return (
    <div className="ribbon">
      <div className="ribbon-tabs" role="tablist">
        {tabs.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={t.id === tab.id}
            className={`ribbon-tab ${t.id === tab.id ? "active" : ""}`}
            onClick={() => setCurrent(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div className="ribbon-body">
        {tab.groups.map((g) => {
          const large = g.actions.filter((a) => a.large);
          const small = g.actions.filter((a) => !a.large);
          const columns: RibbonAction[][] = [];
          for (let i = 0; i < small.length; i += 3) columns.push(small.slice(i, i + 3));
          return (
            <div className="ribbon-group" key={g.label}>
              <div className="ribbon-group-actions">
                {large.map((a) => (
                  <button
                    key={a.id}
                    className={`rb-large ${a.active ? "on" : ""}`}
                    onClick={a.onClick}
                    disabled={a.disabled}
                    title={a.label}
                  >
                    <a.Icon size={26} color={a.disabled ? undefined : a.color} strokeWidth={1.75} />
                    <span>{a.label}</span>
                  </button>
                ))}
                {columns.map((col, i) => (
                  <div className="rb-column" key={i}>
                    {col.map((a) => (
                      <button
                        key={a.id}
                        className={`rb-small ${a.active ? "on" : ""}`}
                        onClick={a.onClick}
                        disabled={a.disabled}
                        title={a.label}
                      >
                        <a.Icon size={15} color={a.disabled ? undefined : a.color} />
                        <span>{a.label}</span>
                      </button>
                    ))}
                  </div>
                ))}
              </div>
              <div className="ribbon-group-label">{g.label}</div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

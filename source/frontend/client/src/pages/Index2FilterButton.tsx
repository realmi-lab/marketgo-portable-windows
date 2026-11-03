import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

export function Index2FilterButton({
  label,
  value,
  options,
  onChange,
  disabled = false,
  triggerLabel,
  showSelection = false,
  defaultValue = "all",
}: {
  label: string;
  triggerLabel?: string;
  value: string;
  options: ReadonlyArray<readonly [string, string]>;
  onChange: (value: string) => void;
  disabled?: boolean;
  showSelection?: boolean;
  defaultValue?: string;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({
    left: 0,
    top: 0,
    width: 200,
    maxHeight: 200,
    zoom: 1,
  });
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const id = useId();
  const selected = options.find(([key]) => key === value)?.[1] ?? value;
  const active = showSelection && value !== defaultValue;
  const portable = !!trigger.current?.closest(".mg-portable");
  const reposition = () => {
    const button = trigger.current;
    if (!button) return;
    const rect = button.getBoundingClientRect();
    const portableRoot = button.closest(".mg-portable");
    const compact = !!portableRoot;
    const zoom = portableRoot
      ? Number.parseFloat(getComputedStyle(portableRoot).zoom) || 1
      : 1;
    const width = compact ? 224 : 200;
    const height = Math.min(
      compact ? 280 : 200,
      options.length * (compact ? 36 : 32),
      (innerHeight - 16) / zoom
    );
    const physicalWidth = width * zoom;
    const physicalHeight = height * zoom;
    const below = rect.bottom + 4;
    setPosition({
      left:
        Math.max(8, Math.min(rect.left, innerWidth - physicalWidth - 8)) / zoom,
      top:
        Math.max(
          8,
          Math.min(
            below + physicalHeight <= innerHeight - 8
              ? below
              : rect.top - physicalHeight - 4,
            innerHeight - physicalHeight - 8
          )
        ) / zoom,
      width,
      maxHeight: height,
      zoom,
    });
  };
  const show = () => {
    reposition();
    setOpen(true);
  };
  useEffect(() => {
    if (!open) return;
    const selectedOption =
      menu.current?.querySelector<HTMLButtonElement>(
        '[aria-selected="true"]'
      ) ?? menu.current?.querySelector<HTMLButtonElement>("button");
    selectedOption?.focus({ preventScroll: true });
    selectedOption?.scrollIntoView({ block: "nearest" });
    const outside = (event: PointerEvent) => {
      if (
        !trigger.current?.contains(event.target as Node) &&
        !menu.current?.contains(event.target as Node)
      )
        setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        trigger.current?.focus();
      }
    };
    const close = () => setOpen(false);
    const scroll = (event: Event) => {
      if (
        !(event.target instanceof Node) ||
        !menu.current?.contains(event.target)
      )
        reposition();
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", scroll, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", scroll, true);
    };
  }, [open]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="index2-filter-button"
        data-active={active ? "" : undefined}
        data-selected-label={showSelection ? "" : undefined}
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        disabled={disabled}
        title={`${label}: ${selected}`}
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={event => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            show();
          }
        }}
      >
        {triggerLabel ?? (active ? `${label} · ${selected}` : label)}
      </button>
      {open &&
        createPortal(
          <div
            ref={menu}
            id={id}
            className={`index2-filter-list${portable ? " portable-filter-menu" : ""}`}
            role="listbox"
            aria-label={`${label} 선택`}
            style={position}
            onKeyDown={event => {
              if (event.key === "Tab") {
                setOpen(false);
                trigger.current?.focus();
                return;
              }
              if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key))
                return;
              event.preventDefault();
              const buttons = [
                ...(menu.current?.querySelectorAll<HTMLButtonElement>(
                  "button"
                ) ?? []),
              ];
              if (!buttons.length) return;
              const index = buttons.indexOf(
                document.activeElement as HTMLButtonElement
              );
              const next =
                event.key === "Home"
                  ? 0
                  : event.key === "End"
                    ? buttons.length - 1
                    : (index +
                        (event.key === "ArrowDown" ? 1 : -1) +
                        buttons.length) %
                      buttons.length;
              buttons[next]?.focus();
            }}
          >
            {options.map(([key, text]) => (
              <button
                type="button"
                role="option"
                aria-label={text}
                aria-selected={key === value}
                key={key}
                onClick={() => {
                  onChange(key);
                  setOpen(false);
                  trigger.current?.focus();
                }}
              >
                {text}
              </button>
            ))}
          </div>,
          document.body
        )}
    </>
  );
}

import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
} from "react";
import { createPortal } from "react-dom";
import { CircleHelp } from "lucide-react";
import { helpPopoverPosition } from "@/lib/help-popover-position";

export function InlineHelp({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const [mainDashboard, setMainDashboard] = useState(false);
  const [position, setPosition] = useState<CSSProperties>({});
  const buttonRef = useRef<HTMLButtonElement>(null);
  const popupRef = useRef<HTMLSpanElement>(null);
  const popupId = useId();

  useLayoutEffect(() => {
    if (!open) return;
    const visualViewport = window.visualViewport;
    const updatePosition = () => {
      const button = buttonRef.current;
      if (!button) return;
      const rect = button.getBoundingClientRect();
      if (mainDashboard) {
        const popup = popupRef.current;
        setPosition({
          ...helpPopoverPosition(
            rect,
            {
              left: visualViewport?.offsetLeft ?? 0,
              top: visualViewport?.offsetTop ?? 0,
              width: visualViewport?.width ?? window.innerWidth,
              height: visualViewport?.height ?? window.innerHeight,
            },
            Math.max(popup?.scrollHeight ?? 0, popup?.offsetHeight ?? 0)
          ),
          visibility: "visible",
        });
        return;
      }
      // Unix and standalone callers retain their existing presentation.
      const width = Math.min(320, window.innerWidth - 24);
      const left = Math.min(
        Math.max(12, rect.left + rect.width / 2 - width / 2),
        window.innerWidth - width - 12
      );
      setPosition({ left, top: rect.bottom + 8, width });
    };
    const closeOnOutsideClick = (event: PointerEvent) => {
      const target = event.target as Node;
      if (
        !buttonRef.current?.contains(target) &&
        !popupRef.current?.contains(target)
      ) {
        setOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        buttonRef.current?.focus();
      }
    };
    const resizeObserver = new ResizeObserver(updatePosition);
    if (mainDashboard && popupRef.current) {
      resizeObserver.observe(popupRef.current);
    }
    updatePosition();
    document.addEventListener("pointerdown", closeOnOutsideClick);
    document.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    visualViewport?.addEventListener("resize", updatePosition);
    visualViewport?.addEventListener("scroll", updatePosition);
    return () => {
      resizeObserver.disconnect();
      document.removeEventListener("pointerdown", closeOnOutsideClick);
      document.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
      visualViewport?.removeEventListener("resize", updatePosition);
      visualViewport?.removeEventListener("scroll", updatePosition);
    };
  }, [open, mainDashboard]);

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className="inline-help"
        aria-label={open ? "설명 닫기" : "설명 보기"}
        aria-expanded={open}
        aria-controls={popupId}
        onClick={() => {
          if (!open) {
            setMainDashboard(!!buttonRef.current?.closest(".main-dashboard"));
            setPosition({ visibility: "hidden" });
          }
          setOpen(current => !current);
        }}
      >
        <CircleHelp aria-hidden="true" />
      </button>
      {open &&
        createPortal(
          <span
            ref={popupRef}
            id={popupId}
            className={`inline-help-popup${mainDashboard ? " main-inline-help-popup" : ""}`}
            role="tooltip"
            style={position}
          >
            {text}
          </span>,
          document.body
        )}
    </>
  );
}

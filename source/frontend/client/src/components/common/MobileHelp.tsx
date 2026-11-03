import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { CircleHelp } from "lucide-react";

/** A touch-operated explanation; the card and its data always stay mounted. */
export function MobileHelp({
  children,
  label,
  allSizes = true,
}: {
  children: ReactNode;
  label: string;
  /** Main-dashboard explanations are available at every screen size. */
  allSizes?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<CSSProperties>({
    visibility: "hidden",
  });
  const buttonRef = useRef<HTMLButtonElement>(null);
  const popupRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const popupId = useId();
  const titleId = `${popupId}-title`;

  const close = useCallback(() => {
    setOpen(false);
    const button = buttonRef.current;
    if (button && button.getClientRects().length > 0) {
      button.focus({ preventScroll: true });
    } else {
      // A mobile-to-desktop resize hides the trigger; do not focus hidden UI.
      button
        ?.closest(".mg, .main-dashboard")
        ?.querySelector<HTMLElement>(".mg-main, .dashboard-content")
        ?.focus({ preventScroll: true });
    }
  }, []);

  useLayoutEffect(() => {
    if (!open) return;
    const mobileViewport = window.matchMedia("(max-width: 767px)");
    const visualViewport = window.visualViewport;
    const updatePosition = () => {
      if (!allSizes && !mobileViewport.matches) {
        close();
        return;
      }
      const button = buttonRef.current;
      if (!button) return;
      const bounds = button.getBoundingClientRect();
      const viewportLeft = visualViewport?.offsetLeft ?? 0;
      const viewportTop = visualViewport?.offsetTop ?? 0;
      const viewportWidth = visualViewport?.width ?? window.innerWidth;
      const viewportHeight = visualViewport?.height ?? window.innerHeight;
      const margin = 12;
      const width = Math.min(360, Math.max(0, viewportWidth - margin * 2));
      const maxHeight = Math.max(0, viewportHeight - margin * 2);
      const height = Math.min(
        popupRef.current?.scrollHeight ?? maxHeight,
        maxHeight
      );
      const minLeft = viewportLeft + margin;
      const maxLeft = viewportLeft + viewportWidth - width - margin;
      const minTop = viewportTop + margin;
      const maxTop = viewportTop + viewportHeight - height - margin;
      const below = bounds.bottom + 8;
      const preferredTop = below <= maxTop ? below : bounds.top - height - 8;
      setPosition({
        position: "fixed",
        left: Math.max(
          minLeft,
          Math.min(bounds.left + bounds.width / 2 - width / 2, maxLeft)
        ),
        top: Math.max(minTop, Math.min(preferredTop, maxTop)),
        width,
        maxHeight,
        visibility: "visible",
      });
    };
    const closeOnOutsidePointer = (event: PointerEvent) => {
      const target = event.target;
      if (
        target instanceof Node &&
        !buttonRef.current?.contains(target) &&
        !popupRef.current?.contains(target)
      ) {
        close();
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close();
      }
    };
    const resizeObserver = new ResizeObserver(updatePosition);
    if (popupRef.current) resizeObserver.observe(popupRef.current);
    updatePosition();
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    mobileViewport.addEventListener("change", updatePosition);
    visualViewport?.addEventListener("resize", updatePosition);
    visualViewport?.addEventListener("scroll", updatePosition);
    return () => {
      resizeObserver.disconnect();
      document.removeEventListener("pointerdown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
      mobileViewport.removeEventListener("change", updatePosition);
      visualViewport?.removeEventListener("resize", updatePosition);
      visualViewport?.removeEventListener("scroll", updatePosition);
    };
  }, [open, close, allSizes]);

  useEffect(() => {
    if (open && position.visibility === "visible") {
      closeButtonRef.current?.focus({ preventScroll: true });
    }
  }, [open, position.visibility]);

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={`mobile-help-trigger hidden${allSizes ? " main-help-all-sizes-trigger" : ""}`}
        aria-label={label}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={popupId}
        onClick={() => {
          if (open) {
            close();
          } else if (
            buttonRef.current?.closest(".mg, .main-dashboard") &&
            (allSizes || window.matchMedia("(max-width: 767px)").matches)
          ) {
            setPosition({ visibility: "hidden" });
            setOpen(true);
          }
        }}
      >
        <CircleHelp aria-hidden="true" />
      </button>
      {open &&
        createPortal(
          <div
            ref={popupRef}
            id={popupId}
            className="main-mobile-help-popup"
            role="dialog"
            aria-labelledby={titleId}
            style={position}
          >
            <div className="main-mobile-help-header">
              <span className="main-mobile-help-title" id={titleId}>
                {label}
              </span>
              <button
                ref={closeButtonRef}
                type="button"
                className="main-mobile-help-close"
                aria-label="설명 닫기"
                onClick={close}
              >
                닫기
              </button>
            </div>
            <div className="main-mobile-help-content">{children}</div>
          </div>,
          document.body
        )}
    </>
  );
}

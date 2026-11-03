import "./mobile-more-toggle.css";

/**
 * Rows past this count are hidden on phones while a list is collapsed. The
 * `.mobile-collapsed > :nth-child(n + 6)` rule in mobile-more-toggle.css uses
 * the same number; change both together.
 */
export const MOBILE_VISIBLE_ROWS = 5;

/** Phone-only button that shows or hides the rest of a long block. */
export function MobileMoreToggle({
  expanded,
  onToggle,
  moreLabel,
}: {
  expanded: boolean;
  onToggle: () => void;
  moreLabel: string;
}) {
  return (
    <button
      type="button"
      className="mobile-more-toggle"
      aria-expanded={expanded}
      onClick={onToggle}
    >
      {expanded ? "접기" : moreLabel}
    </button>
  );
}

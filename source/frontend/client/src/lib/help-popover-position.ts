type AnchorBounds = {
  left: number;
  top: number;
  bottom: number;
  width: number;
};

type VisibleViewport = {
  left: number;
  top: number;
  width: number;
  height: number;
};

/** Keep a measured help popover inside the visible viewport, including keyboards. */
export function helpPopoverPosition(
  anchor: AnchorBounds,
  viewport: VisibleViewport,
  contentHeight: number
) {
  const margin = Math.min(12, viewport.width / 2, viewport.height / 2);
  const width = Math.min(320, Math.max(0, viewport.width - margin * 2));
  const maxHeight = Math.max(0, viewport.height - margin * 2);
  const height = Math.min(Math.max(0, contentHeight), maxHeight);
  const minLeft = viewport.left + margin;
  const maxLeft = viewport.left + viewport.width - width - margin;
  const minTop = viewport.top + margin;
  const maxTop = viewport.top + viewport.height - height - margin;
  const below = anchor.bottom + 8;
  const preferredTop = below <= maxTop ? below : anchor.top - height - 8;

  return {
    left: Math.max(
      minLeft,
      Math.min(anchor.left + anchor.width / 2 - width / 2, maxLeft)
    ),
    top: Math.max(minTop, Math.min(preferredTop, maxTop)),
    width,
    maxHeight,
  };
}

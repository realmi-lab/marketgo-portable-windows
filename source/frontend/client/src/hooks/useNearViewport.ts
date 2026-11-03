import { useEffect, useState, type RefObject } from "react";

/**
 * Whether the element is within `rootMargin` of the viewport. Unlike
 * DeferredRender it keeps following the element, so a section can pause work
 * while it is scrolled far away and resume when it comes back.
 */
export function useNearViewport(
  ref: RefObject<Element | null>,
  rootMargin = "600px 0px"
): boolean {
  const [near, setNear] = useState(true);
  useEffect(() => {
    const node = ref.current;
    if (!node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      entries => setNear(entries.some(entry => entry.isIntersecting)),
      { rootMargin }
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [ref, rootMargin]);
  return near;
}

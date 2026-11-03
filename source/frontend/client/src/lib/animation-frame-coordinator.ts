export type AnimationFrameScheduler = {
  request: (callback: FrameRequestCallback) => number;
  cancel: (handle: number) => void;
};

type AnimationFrameSubscriber = (now: number) => boolean;

export function createAnimationFrameCoordinator(
  scheduler: AnimationFrameScheduler
) {
  const subscribers = new Set<AnimationFrameSubscriber>();
  let frameHandle: number | null = null;

  const schedule = () => {
    if (frameHandle !== null || subscribers.size === 0) return;
    frameHandle = scheduler.request(now => {
      frameHandle = null;
      for (const subscriber of [...subscribers]) {
        if (!subscriber(now)) subscribers.delete(subscriber);
      }
      schedule();
    });
  };

  return (subscriber: AnimationFrameSubscriber) => {
    subscribers.add(subscriber);
    schedule();
    return () => {
      subscribers.delete(subscriber);
      if (subscribers.size === 0 && frameHandle !== null) {
        scheduler.cancel(frameHandle);
        frameHandle = null;
      }
    };
  };
}

const subscribeSharedAnimationFrame = createAnimationFrameCoordinator({
  request: callback => globalThis.requestAnimationFrame(callback),
  cancel: handle => globalThis.cancelAnimationFrame(handle),
});

export { subscribeSharedAnimationFrame };

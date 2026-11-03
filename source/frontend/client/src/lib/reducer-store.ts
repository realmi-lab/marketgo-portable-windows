/**
 * A value kept outside React's page state and changed by each frame through
 * `reduce`, so only components that read it (useSyncExternalStore) render
 * for every frame, and no frame is lost while React is between renders.
 */
export interface ReducerStore<State, Frame> {
  getSnapshot: () => State;
  subscribe: (listener: () => void) => () => void;
  publish: (frame: Frame) => void;
}

export function createReducerStore<State, Frame>(
  initial: State,
  reduce: (state: State, frame: Frame) => State
): ReducerStore<State, Frame> {
  let state = initial;
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => state,
    subscribe: listener => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    publish: frame => {
      const next = reduce(state, frame);
      if (next === state) return;
      state = next;
      listeners.forEach(listener => listener());
    },
  };
}

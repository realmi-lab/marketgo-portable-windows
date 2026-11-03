// Bound both response headers and body consumption; keep the caller's earlier
// cancellation. Mutating/model requests retain their own operation budgets.
export function fetchRead(
  input: string,
  init: RequestInit = {},
  timeoutMS = 12_000
): Promise<Response> {
  const deadline = AbortSignal.timeout(timeoutMS);
  return fetch(input, {
    ...init,
    signal: init.signal ? AbortSignal.any([init.signal, deadline]) : deadline,
  });
}

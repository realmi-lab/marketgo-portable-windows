// Browser transport state is not proof that exchange collection has stopped.
// Retained values are explicitly described as old, never as a live fallback.
export function marketShareReconnectMessage(hasRetainedValues: boolean): string {
  return hasRetainedValues
    ? "화면 연결 복구 중 · 마지막 수신값을 표시하고 있습니다. 연결되면 자동으로 갱신됩니다."
    : "화면 연결 복구 중입니다. 연결되면 새 자료를 표시합니다.";
}

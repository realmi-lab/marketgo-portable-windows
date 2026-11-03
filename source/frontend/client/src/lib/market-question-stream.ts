import type {
  GroundedMarketQuestionConversationScope,
  GroundedMarketQuestionResponse,
  GroundedMarketQuestionStageEvent,
} from "./market-share-types.ts";

// The backend permits a bounded 2 MiB evidence bundle. Three staged envelopes
// may repeat that immutable bundle, so keep a small transport margin.
const MAX_STAGE_STREAM_BYTES = 8 * 1024 * 1024;
const MAX_STAGE_EVENTS = 3;

export interface GroundedMarketQuestionStreamResult {
  response: GroundedMarketQuestionResponse;
  status: GroundedMarketQuestionStageEvent["status"];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasFiniteOptionalTimestamps(
  boundary: Record<string, unknown>,
  keys: string[]
): boolean {
  return keys.every(key => {
    const value = boundary[key];
    return value === undefined || (Number.isFinite(value) && Number(value) > 0);
  });
}

export function isGroundedMarketQuestionConversationScope(
  payload: unknown
): payload is GroundedMarketQuestionConversationScope {
  if (!isRecord(payload) || !isRecord(payload.query) || !isRecord(payload.revision))
    return false;
  return (
    payload.contract === "GROUNDED_MARKET_QUESTION_SCOPE_V1" &&
    typeof payload.token === "string" &&
    payload.token.trim().length > 0 &&
    typeof payload.evidence_bundle_id === "string" &&
    payload.evidence_bundle_id.trim().length > 0 &&
    Number.isFinite(payload.created_at_ms) &&
    Number(payload.created_at_ms) > 0 &&
    ["FULFILLED", "PARTIAL", "UNSUPPORTED", "NEEDS_CLARIFICATION"].includes(
      String(payload.fulfillment_status)
    ) &&
    Number.isFinite(payload.as_of_ms) &&
    Number(payload.as_of_ms) > 0 &&
    ["CONFIRMED", "UNAVAILABLE", "NOT_APPLICABLE"].includes(
      String(payload.revision.status)
    )
  );
}

export function isGroundedMarketQuestionResponse(
  payload: unknown
): payload is GroundedMarketQuestionResponse {
  if (!payload || typeof payload !== "object") return false;
  const response = payload as GroundedMarketQuestionResponse;
  return (
    response.contract === "GROUNDED_MARKET_QUESTION_V2" &&
    typeof response.facts?.evidence_layer === "string" &&
    typeof response.quality?.status === "string" &&
    typeof response.prose?.answer === "string" &&
    Array.isArray(response.prose.key_points) &&
    typeof response.provider?.source === "string"
  );
}

function isStageEvent(payload: unknown): payload is GroundedMarketQuestionStageEvent {
  if (!payload || typeof payload !== "object") return false;
  const event = payload as GroundedMarketQuestionStageEvent;
  return (
    Number.isInteger(event.sequence) &&
    event.sequence > 0 &&
    ["QUANTITATIVE_READY", "EXPLANATION_READY", "TERMINAL"].includes(
      event.stage
    ) &&
    [
      "READY",
      "ACCEPTED",
      "PARTIAL",
      "FAILED",
      "TIMEOUT",
      "CANCELED",
      "UNVERIFIED",
    ].includes(event.status) &&
    Number.isFinite(event.emitted_at_ms) &&
    isGroundedMarketQuestionResponse(event.response)
  );
}

function stageResponseContractIsValid(
  response: GroundedMarketQuestionResponse
): boolean {
  const bundle = response.evidence_bundle;
  const orchestration = response.orchestration;
  const scope = response.conversation_scope;
  if (
    !bundle ||
    bundle.contract_version !== "MARKET_QUESTION_EVIDENCE_BUNDLE_V1" ||
    !bundle.bundle_id?.trim() ||
    !bundle.catalog_version?.trim() ||
    !Number.isFinite(bundle.created_at_ms) ||
    bundle.created_at_ms <= 0 ||
    !bundle.fulfillment ||
    !["FULFILLED", "PARTIAL", "UNSUPPORTED", "NEEDS_CLARIFICATION"].includes(
      bundle.fulfillment.status
    ) ||
    !Array.isArray(bundle.fulfillment.requirements) ||
    !bundle.scope ||
    !["EXACT", "SUBSTITUTED_PARTIAL", "UNAVAILABLE"].includes(
      bundle.scope.status
    ) ||
    (bundle.scope.included_exchanges !== null &&
      !Array.isArray(bundle.scope.included_exchanges)) ||
    (bundle.scope.excluded_exchanges !== null &&
      !Array.isArray(bundle.scope.excluded_exchanges)) ||
    !orchestration ||
    !["SUPPORTED", "CONFLICT", "UNDETERMINED"].includes(
      orchestration.validation_status
    ) ||
    typeof orchestration.explanation_status !== "string" ||
    !Array.isArray(orchestration.blocks) ||
    !scope ||
    !isGroundedMarketQuestionConversationScope(scope)
  )
    return false;
  return (
    scope.evidence_bundle_id === bundle.bundle_id &&
    scope.created_at_ms === bundle.created_at_ms &&
    scope.fulfillment_status === bundle.fulfillment.status &&
    scope.as_of_ms === response.provider.as_of_ms &&
    JSON.stringify(scope.query) === JSON.stringify(response.query) &&
    JSON.stringify(scope.revision) === JSON.stringify(bundle.revision)
  );
}

function evidenceBinding(response: GroundedMarketQuestionResponse): string {
  const bundle = response.evidence_bundle;
  const requested = bundle?.time?.requested;
  const actual = bundle?.time?.actual;
  if (
    !bundle?.bundle_id ||
    !Number.isFinite(bundle.created_at_ms) ||
    !bundle.time?.basis ||
    !bundle.time.aggregation_semantics ||
    !requested ||
    !actual ||
    !["CONFIRMED", "UNAVAILABLE", "NOT_APPLICABLE"].includes(
      bundle.time.status
    ) ||
    !hasFiniteOptionalTimestamps(requested, ["start_ms", "end_ms", "as_of_ms"]) ||
    !hasFiniteOptionalTimestamps(actual, [
      "start_ms",
      "end_ms",
      "as_of_ms",
      "observed_at_ms",
    ]) ||
    (bundle.time.status === "CONFIRMED" &&
      ![actual.start_ms, actual.end_ms, actual.as_of_ms].some(Number.isFinite)) ||
    (bundle.time.basis === "ROLLING_24H_SNAPSHOT" &&
      bundle.time.status === "CONFIRMED" &&
      !Number.isFinite(actual.as_of_ms)) ||
    (bundle.time.status === "UNAVAILABLE" && !bundle.time.reason?.trim())
  )
    return "";
  return [
    bundle.bundle_id,
    bundle.created_at_ms,
    JSON.stringify(bundle.time),
  ].join(":");
}

function quantitativeFingerprint(
  response: GroundedMarketQuestionResponse
): string {
  return JSON.stringify({
    query: response.query,
    facts: response.facts,
    quality: response.quality,
    provenance: response.provenance,
    evidence_bundle: response.evidence_bundle,
    conversation_scope: response.conversation_scope,
  });
}

function withAnchoredQuantitativeResponse(
  response: GroundedMarketQuestionResponse,
  quantitative: GroundedMarketQuestionResponse
): GroundedMarketQuestionResponse {
  return {
    ...response,
    query: quantitative.query,
    facts: quantitative.facts,
    quality: quantitative.quality,
    provenance: quantitative.provenance,
    evidence_bundle: quantitative.evidence_bundle,
    conversation_scope: quantitative.conversation_scope,
  };
}

export async function readGroundedMarketQuestionStageStream(
  response: Response,
  onEvent: (event: GroundedMarketQuestionStageEvent) => void
): Promise<GroundedMarketQuestionStreamResult> {
  if (!response.body) throw new Error("Grounded question stream is unavailable");

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let receivedBytes = 0;
  let lastSequence = 0;
  let immutableBinding = "";
  let quantitative: GroundedMarketQuestionResponse | null = null;
  let previousStage: GroundedMarketQuestionStageEvent["stage"] | null = null;
  let eventCount = 0;
  const streamState: { terminal: GroundedMarketQuestionStageEvent | null } = {
    terminal: null,
  };

  const acceptLine = (line: string) => {
    if (!line.trim()) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(line);
    } catch {
      throw new Error("Grounded question stream contains invalid JSON");
    }
    if (!isStageEvent(parsed))
      throw new Error("Grounded question stream event is invalid");
    let payload: GroundedMarketQuestionStageEvent = parsed;
    if (!stageResponseContractIsValid(payload.response))
      throw new Error("Grounded question stage response contract is invalid");
    eventCount += 1;
    if (eventCount > MAX_STAGE_EVENTS)
      throw new Error("Grounded question stream has too many events");
    if (payload.sequence <= lastSequence)
      throw new Error("Grounded question stream sequence is invalid");
    lastSequence = payload.sequence;

    if (
      (!previousStage && payload.stage !== "QUANTITATIVE_READY") ||
      (previousStage === "QUANTITATIVE_READY" &&
        !["EXPLANATION_READY", "TERMINAL"].includes(payload.stage)) ||
      (previousStage === "EXPLANATION_READY" && payload.stage !== "TERMINAL")
    )
      throw new Error("Grounded question stream stage transition is invalid");

    const binding = evidenceBinding(payload.response);
    if (!binding)
      throw new Error("Grounded question stage is missing its evidence bundle");
    if (immutableBinding && immutableBinding !== binding)
      throw new Error("Grounded question evidence changed between stages");
    immutableBinding = binding;

    if (payload.stage === "QUANTITATIVE_READY") {
      quantitative = payload.response;
    } else {
      if (!quantitative)
        throw new Error("Grounded question explanation has no quantitative stage");
      if (
        quantitativeFingerprint(payload.response) !==
        quantitativeFingerprint(quantitative)
      )
        throw new Error("Grounded question quantitative result changed between stages");
      payload = {
        ...payload,
        response: withAnchoredQuantitativeResponse(payload.response, quantitative),
      };
    }

    if (streamState.terminal)
      throw new Error("Grounded question stream continued after terminal state");
    onEvent(payload);
    previousStage = payload.stage;
    if (payload.stage === "TERMINAL") streamState.terminal = payload;
  };

  try {
    while (true) {
      const chunk = await reader.read();
      if (chunk.done) break;
      receivedBytes += chunk.value.byteLength;
      if (receivedBytes > MAX_STAGE_STREAM_BYTES)
        throw new Error("Grounded question stream exceeded its size limit");
      buffer += decoder.decode(chunk.value, { stream: true });
      let newline = buffer.indexOf("\n");
      while (newline >= 0) {
        acceptLine(buffer.slice(0, newline));
        buffer = buffer.slice(newline + 1);
        newline = buffer.indexOf("\n");
      }
    }
    buffer += decoder.decode();
    acceptLine(buffer);
    if (!streamState.terminal)
      throw new Error("Grounded question stream ended before terminal state");
    return {
      response: streamState.terminal.response,
      status: streamState.terminal.status,
    };
  } catch (error) {
    await reader.cancel(error).catch(() => {});
    throw error;
  } finally {
    reader.releaseLock();
  }
}

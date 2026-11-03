import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { readGroundedMarketQuestionStageStream } from "./market-question-stream.ts";
import type {
  GroundedMarketQuestionResponse,
  GroundedMarketQuestionStageEvent,
  GroundedMarketQuestionEvidenceBundle,
} from "./market-share-types.ts";

// Only the cross-language case needs the Go snapshot. Calculator lineages
// without the staged evidence bundle do not ship that file, so read it
// optionally and skip that one case instead of failing the whole parser suite.
const goSnapshotPath = new URL(
  "../../../../internal/app/testdata/market_question_evidence_current_snapshot.json",
  import.meta.url
);
const goMarshaledCurrentBundle = (() => {
  try {
    return JSON.parse(
      readFileSync(goSnapshotPath, "utf8")
    ) as GroundedMarketQuestionEvidenceBundle;
  } catch {
    return null;
  }
})();

function response(
  bundleID = "bundle-ui-stage-1",
  answer = "",
  shareDelta = 1.25
): GroundedMarketQuestionResponse {
  const query = { requested_explanation: [] };
  const revision = {
    status: "UNAVAILABLE" as const,
    reason: "fixture has no retained input revision",
  };
  return {
    contract: "GROUNDED_MARKET_QUESTION_V2",
    query,
    facts: { evidence_layer: "UI_STAGE_FIXTURE", share_delta_pp: shareDelta },
    quality: { status: "LIVE" },
    prose: { answer, key_points: [], caveat: "" },
    provider: { source: answer ? "HERMES" : "QUANTITATIVE", as_of_ms: 100 },
    provenance: { answer_policy_version: "UI_STAGE_FIXTURE" },
    evidence_bundle: {
      contract_version: "MARKET_QUESTION_EVIDENCE_BUNDLE_V1",
      bundle_id: bundleID,
      catalog_version: "MARKET_QUESTION_CATALOG_V1",
      created_at_ms: 90,
      fulfillment: { status: "FULFILLED", requirements: [] },
      time: {
        basis: "ROLLING_24H_SNAPSHOT",
        aggregation_semantics: "SNAPSHOT_LEVEL",
        status: "CONFIRMED",
        requested: {},
        actual: { start_ms: 1, end_ms: 60, as_of_ms: 100 },
      },
      scope: {
        status: "EXACT",
        included_exchanges: ["BITHUMB"],
        excluded_exchanges: [],
      },
      revision,
    },
    orchestration: {
      mode: "QUANTITATIVE_FIRST_A_PLUS_C_V1",
      overall_status: answer ? "COMPLETE" : "PENDING_EXPLANATION",
      quantitative_status: "FULFILLED",
      explanation_status: answer ? "ACCEPTED" : "PENDING",
      validation_status: answer ? "SUPPORTED" : "UNDETERMINED",
      blocks: [],
    },
    conversation_scope: {
      contract: "GROUNDED_MARKET_QUESTION_SCOPE_V1",
      token: "opaque-fixture-token",
      query,
      evidence_bundle_id: bundleID,
      revision,
      created_at_ms: 90,
      fulfillment_status: "FULFILLED",
      as_of_ms: 100,
    },
  } as unknown as GroundedMarketQuestionResponse;
}

function event(
  sequence: number,
  stage: GroundedMarketQuestionStageEvent["stage"],
  stageResponse: GroundedMarketQuestionResponse,
  status: GroundedMarketQuestionStageEvent["status"] = "READY"
): GroundedMarketQuestionStageEvent {
  return {
    sequence,
    stage,
    status,
    emitted_at_ms: 100 + sequence,
    response: stageResponse,
  };
}

function streamResponse(events: GroundedMarketQuestionStageEvent[]): Response {
  return new Response(events.map(value => JSON.stringify(value)).join("\n"), {
    headers: { "Content-Type": "application/x-ndjson" },
  });
}

test("staged answers keep the first quantitative result through validated prose", async () => {
  const first = response();
  const explained = response("bundle-ui-stage-1", "검증된 화면 설명");
  const seen: GroundedMarketQuestionStageEvent[] = [];
  const result = await readGroundedMarketQuestionStageStream(
    streamResponse([
      event(1, "QUANTITATIVE_READY", first),
      event(2, "EXPLANATION_READY", explained, "ACCEPTED"),
      event(3, "TERMINAL", explained, "ACCEPTED"),
    ]),
    value => seen.push(value)
  );

  assert.equal(seen.length, 3);
  assert.equal(result.response.prose.answer, "검증된 화면 설명");
  assert.equal(result.response.facts, seen[0].response.facts);
  assert.equal(
    result.response.evidence_bundle,
    seen[0].response.evidence_bundle
  );
});

test("a changed evidence identity or changed facts cannot replace visible cards", async () => {
  const changedQuery = response("bundle-ui-stage-1", "표시하면 안 되는 설명");
  changedQuery.query = {
    ...changedQuery.query,
    metric: "DIFFERENT_METRIC",
  };
  changedQuery.conversation_scope!.query = changedQuery.query;
  for (const changed of [
    response("different-bundle", "표시하면 안 되는 설명"),
    response("bundle-ui-stage-1", "표시하면 안 되는 설명", -9),
    changedQuery,
  ]) {
    const seen: GroundedMarketQuestionStageEvent[] = [];
    await assert.rejects(
      readGroundedMarketQuestionStageStream(
        streamResponse([
          event(1, "QUANTITATIVE_READY", response()),
          event(2, "EXPLANATION_READY", changed, "ACCEPTED"),
        ]),
        value => seen.push(value)
      ),
      /evidence changed|quantitative result changed/
    );
    assert.deepEqual(
      seen.map(value => value.stage),
      ["QUANTITATIVE_READY"]
    );
  }
});

test("terminal-first, duplicate stages, and EOF without terminal fail closed", async () => {
  const invalidStreams = [
    [event(1, "TERMINAL", response(), "FAILED")],
    [
      event(1, "QUANTITATIVE_READY", response()),
      event(2, "QUANTITATIVE_READY", response()),
    ],
    [
      event(2, "QUANTITATIVE_READY", response()),
      event(1, "TERMINAL", response(), "FAILED"),
    ],
    [event(1, "QUANTITATIVE_READY", response())],
  ];
  for (const events of invalidStreams) {
    await assert.rejects(
      readGroundedMarketQuestionStageStream(streamResponse(events), () => {}),
      /stage transition|sequence is invalid|ended before terminal/
    );
  }
});

test("malformed bundle input cancels the stream reader before any UI event", async () => {
  let canceled = false;
  const malformed = event(1, "QUANTITATIVE_READY", response());
  malformed.response.evidence_bundle = {
    bundle_id: "missing-time",
  } as GroundedMarketQuestionResponse["evidence_bundle"];
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(`${JSON.stringify(malformed)}\n`));
    },
    cancel() {
      canceled = true;
    },
  });
  let seen = 0;
  await assert.rejects(
    readGroundedMarketQuestionStageStream(new Response(body), () => {
      seen += 1;
    }),
    /stage response contract is invalid|missing its evidence bundle/
  );
  assert.equal(seen, 0);
  assert.equal(canceled, true);
});

test("Go-marshaled current snapshot may omit interval boundaries without inventing zeroes", {
  skip: goMarshaledCurrentBundle
    ? false
    : "this checkout's calculator has no staged evidence-bundle snapshot",
}, async () => {
  const snapshot = goMarshaledCurrentBundle!;
  const current = response();
  current.evidence_bundle = snapshot;
  current.provider.as_of_ms = snapshot.time.actual.as_of_ms!;
  current.conversation_scope = {
    ...current.conversation_scope!,
    evidence_bundle_id: snapshot.bundle_id,
    revision: snapshot.revision,
    created_at_ms: snapshot.created_at_ms,
    fulfillment_status: snapshot.fulfillment.status,
    as_of_ms: snapshot.time.actual.as_of_ms!,
  };
  const terminal = response(snapshot.bundle_id, "현재 설명");
  terminal.evidence_bundle = current.evidence_bundle;
  terminal.provider.as_of_ms = current.provider.as_of_ms;
  terminal.conversation_scope = current.conversation_scope;
  const result = await readGroundedMarketQuestionStageStream(
    streamResponse([
      event(1, "QUANTITATIVE_READY", current),
      event(2, "TERMINAL", terminal, "ACCEPTED"),
    ]),
    () => {}
  );
  assert.equal(result.response.facts.share_delta_pp, 1.25);
  assert.equal(result.response.evidence_bundle?.time.actual.start_ms, undefined);
  assert.equal(result.response.evidence_bundle?.time.actual.end_ms, undefined);
  assert.equal(
    result.response.evidence_bundle?.time.actual.as_of_ms,
    1800000000000
  );
});

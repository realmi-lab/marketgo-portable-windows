import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  MARKET_QUESTION_REQUEST_TIMEOUT_MS,
  marketQuestionRequestTimeoutMS,
  NOTICE_QUESTION_REQUEST_TIMEOUT_MS,
  QUESTION_TIMEOUT_MESSAGE,
  QuestionRequestError,
  QuestionRequestGate,
  questionRequestErrorMessage,
  readQuestionRequestError,
} from "./question-request.ts";

test("five-minute question budgets include response delivery in both builds", () => {
  assert.equal(marketQuestionRequestTimeoutMS(true), 310_000);
  assert.equal(marketQuestionRequestTimeoutMS(false), 310_000);
  assert.equal(NOTICE_QUESTION_REQUEST_TIMEOUT_MS, 310_000);
});

test("staged market questions bypass proxy buffering and the generic 60-second route", () => {
  const nginx = readFileSync(
    new URL("../../../deploy/nginx.conf", import.meta.url),
    "utf8"
  );
  assert.match(
    nginx,
    /location = \/api\/market-share\/grounded-question\/stream \{[\s\S]*?proxy_read_timeout 330s;[\s\S]*?proxy_buffering off;[\s\S]*?\n    \}/
  );
  assert.match(
    nginx,
    /location ~ \^\/api\/market-share\/[\s\S]*?proxy_read_timeout 330s;/
  );
  for (const name of ["nginx.conf", "nginx.notice-llm2.conf"]) {
    const config = readFileSync(
      new URL(`../../../deploy/${name}`, import.meta.url),
      "utf8"
    );
    assert.match(config, /server_name share\.realmi\.net notice\.realmi\.net /);
  }
});

test("both question owners stay pending for five minutes and fence late responses", context => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  for (const budget of [
    MARKET_QUESTION_REQUEST_TIMEOUT_MS,
    NOTICE_QUESTION_REQUEST_TIMEOUT_MS,
  ]) {
    const gate = new QuestionRequestGate();
    let timedOut = false;
    const request = gate.start(budget, () => {
      timedOut = true;
    })!;
    context.mock.timers.tick(300_000);
    assert.equal(timedOut, false);
    assert.equal(request.signal.aborted, false);
    context.mock.timers.tick(9_999);
    assert.equal(timedOut, false);
    context.mock.timers.tick(1);
    assert.equal(timedOut, true);
    assert.equal(request.signal.aborted, true);
    assert.equal(request.finish(), false);
  }
});

test("cancellation fences an old completion and does not cancel the next request", () => {
  const requests = new QuestionRequestGate();
  const first = requests.start(60_000, () =>
    assert.fail("unexpected timeout")
  )!;
  assert.equal(
    requests.start(60_000, () => {}),
    null,
    "duplicate click must not start work"
  );
  assert.equal(requests.cancel(), true);
  assert.equal(first.signal.aborted, true);
  const second = requests.start(60_000, () =>
    assert.fail("unexpected timeout")
  )!;
  assert.equal(first.finish(), false, "a cancelled response must not publish");
  assert.equal(second.signal.aborted, false);
  assert.equal(second.finish(), true);
  assert.equal(second.finish(), false, "completion can publish only once");
  assert.equal(requests.cancel(), false);
});

test("deadline releases the UI even when transport ignores abort and its late response is rejected", context => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const requests = new QuestionRequestGate();
  let timeouts = 0;
  const first = requests.start(
    MARKET_QUESTION_REQUEST_TIMEOUT_MS,
    () => timeouts++
  )!;
  context.mock.timers.tick(MARKET_QUESTION_REQUEST_TIMEOUT_MS - 1);
  assert.equal(timeouts, 0);
  context.mock.timers.tick(1);
  assert.equal(timeouts, 1);
  assert.equal(first.signal.aborted, true);
  assert.equal(first.finish(), false);
  const second = requests.start(
    NOTICE_QUESTION_REQUEST_TIMEOUT_MS,
    () => timeouts++
  )!;
  assert.equal(second.finish(), true);
  context.mock.timers.tick(NOTICE_QUESTION_REQUEST_TIMEOUT_MS);
  assert.equal(timeouts, 1, "completed request must not time out later");
  assert.equal(MARKET_QUESTION_REQUEST_TIMEOUT_MS, 310_000);
  assert.equal(NOTICE_QUESTION_REQUEST_TIMEOUT_MS, 310_000);
});

test("422 asks for a changed question while 503 asks for retry, without exposing raw diagnostics", async () => {
  const restricted = await readQuestionRequestError(
    new Response(
      JSON.stringify({
        detail:
          "database internals and data-completeness questions are restricted",
      }),
      { status: 422 }
    )
  );
  assert.match(questionRequestErrorMessage(restricted), /질문을 바꿔/);
  assert.doesNotMatch(restricted.message, /잠시 뒤|database|restricted/);
  const invalid = await readQuestionRequestError(
    new Response(
      JSON.stringify({
        detail: "market question must contain between 2 and 500 characters",
      }),
      { status: 422 }
    )
  );
  assert.match(invalid.message, /2~500자/);
  const unavailable = await readQuestionRequestError(
    new Response("upstream internal error", { status: 503 })
  );
  assert.match(unavailable.message, /잠시 뒤 다시/);
  assert.doesNotMatch(unavailable.message, /upstream/);
  assert.equal(new QuestionRequestError(504).message, QUESTION_TIMEOUT_MESSAGE);
  assert.match(
    questionRequestErrorMessage(new TypeError("secret network diagnostic")),
    /연결/
  );
  assert.match(
    questionRequestErrorMessage(
      new Error("Grounded question evidence changed between stages")
    ),
    /설명을 표시하지 않았습니다/
  );
});

test("both UI owners fence requests and clearing history resets the persistent task owner", () => {
  const read = (path: string) =>
    readFileSync(new URL(path, import.meta.url), "utf8");
  const context = read("../contexts/MarketQuestionTaskContext.tsx");
  const market = read("../components/HermesMarketQuestion.tsx");
  const notices = read("../components/HermesNoticeQuestion.tsx");
  assert.match(
    context,
    /askGroundedMarketQuestionStream\([\s\S]*?question,[\s\S]*?history,[\s\S]*?conversationScope/
  );
  assert.match(
    context,
    /const clearTask[\s\S]*?requests.current.cancel\(\);[\s\S]*?setTask\(\{ status: "idle" \}\)/
  );
  assert.match(market, /clearTask\(\);\s*setConversation\(\[\]\)/);
  assert.match(market, /setError\(task.message\)/);
  assert.match(
    notices,
    /const selectPersona[\s\S]*?requests.current.cancel\(\)/
  );
  assert.match(
    notices,
    /if \(!request.finish\(\)\) return;\s*contextSourceIDs.current/
  );
  assert.match(notices, /답변한 질문: \{answerQuestion\}/);
  assert.match(notices, /답변 역할:/);
  assert.match(
    notices,
    /<ReactMarkdown[\s\S]*?remarkPlugins=\{\[remarkGfm\]\}/
  );
  assert.match(notices, /a: \(\{ children \}\) => <span>\{children\}<\/span>/);
  assert.match(notices, /img: \(\{ alt \}\) => <span>/);
  assert.doesNotMatch(
    notices,
    /rehypeRaw|rehype-raw|dangerouslySetInnerHTML|skipHtml/
  );
});

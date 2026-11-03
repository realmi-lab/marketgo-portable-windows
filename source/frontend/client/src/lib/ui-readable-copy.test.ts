import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import test from "node:test";

const libDir = dirname(fileURLToPath(import.meta.url));
const clientDir = dirname(libDir);

function tsxFiles(root: string): string[] {
  return readdirSync(root, { withFileTypes: true }).flatMap(entry => {
    const path = join(root, entry.name);
    if (entry.isDirectory()) return tsxFiles(path);
    return entry.isFile() && entry.name.endsWith(".tsx") ? [path] : [];
  });
}

const uiSources = ["pages", "components"]
  .flatMap(directory => tsxFiles(join(clientDir, directory)))
  .map(path => ({ path, source: readFileSync(path, "utf8") }));

const forbiddenCopy: Array<[string, RegExp]> = [
  ["데이터베이스 구현 용어", /PostgreSQL 구간|현재 snapshot|DB·SQL/],
  ["전송 방식 용어", /거래소 WebSocket|WebSocket 단기/],
  ["공식 자료의 내부 이름", /공식 티커|rolling 24시간 티커/],
  ["어려운 시간 표현", /동일시각|순변화/],
  ["어려운 기여 표현", /관측 기여|보조 정황/],
  ["수집 내부 용어", /연결 조각|파싱 오류|큐 경계|처리 보존 오차/],
  ["이미지 글자 읽기 내부 용어", /OCR (?:분석|사용 불가|오류|상세|텍스트)/],
  ["보고서 구현 용어", /Markdown 원문/],
  ["호가 내부 용어", /최우선가/],
  ["가격 제외 내부 용어", /가격 검역/],
  ["쉬운 설명 없는 호가 전문어", /10호가/],
  ["쉬운 설명 없는 흐름 전문어", /순기여도|신규·만료/],
  ["어려운 준비 상태", /미초기화|저활동/],
  ["분 단위 자료의 전문어", /공식 (?:1|10)분봉/],
  ["시간 자료의 전문어", /완료된 1시간봉|동일 종료시각/],
  ["원시 근거 상태 코드", /증거 수준/],
  ["분석 내부 용어", /보조 진단 브리핑/],
  ["자료 누락 내부 용어", /원관측 공백|미관측 구간|실시간 관측 공백 이력/],
  ["준비 상태 내부 용어", /레지스트리 대기/],
];

test("keeps general dashboard copy in plain Korean", () => {
  for (const { path, source } of uiSources) {
    for (const [reason, pattern] of forbiddenCopy) {
      assert.doesNotMatch(source, pattern, `${reason}: ${path}`);
    }
  }
});

// Copy corrected on 2026-09-30. Labels built in lib/ and the Unix screen are
// checked too, because they reach the same pages.
function sourceFiles(root: string, extension: string): string[] {
  return readdirSync(root, { withFileTypes: true }).flatMap(entry => {
    const path = join(root, entry.name);
    if (entry.isDirectory()) return sourceFiles(path, extension);
    return entry.isFile() &&
      entry.name.endsWith(extension) &&
      !entry.name.endsWith(".test.ts")
      ? [path]
      : [];
  });
}

const displaySources = [
  ...uiSources,
  ...sourceFiles(libDir, ".ts"),
  ...sourceFiles(join(dirname(clientDir), "..", "unix_share", "src"), ".tsx"),
]
  .map(entry =>
    typeof entry === "string"
      ? { path: entry, source: readFileSync(entry, "utf8") }
      : entry
  )
  .map(({ path, source }) => ({
    path,
    // Old server wording kept only to rewrite stored text is not display copy.
    source: source.replace(
      /\/\* legacy server text: begin \*\/[\s\S]*?\/\* legacy server text: end \*\//g,
      ""
    ),
  }));

const awkwardCopy: Array<[string, RegExp]> = [
  ["받침에 따라 바뀌는 조사를 고정", /\}(?:과|와|을|를) 직전/],
  ["원인을 단정하는 것처럼 읽히는 공지 표기", /공지 영향 아님/],
  ["MATURE의 직역", /성숙 반응|성숙도/],
  ["관측 상태의 직역", /관측 조건 충족/],
  ["기여를 풀어 쓴 어색한 표현", /보탠 값|보탠 계산값/],
  ["window의 직역", /시간창|창의 빗썸|48시간 창/],
  ["필터 결과를 뜻하는 표기", /조건 결과/],
  [
    "행정 문서식 확보 표현",
    /(?:시각|링크|발췌|상태|금액|자산|데이터|호가|내용|경과|기여값|비교|시작점) 미확보/,
  ],
  ["같은 뜻의 다른 이름", /체결횟수|고유 코인|국내 5사/],
  ["띄어쓰기", /종료시각|원화마켓|기타마켓|억원/],
  [
    "내부 구현 용어",
    /SQLite|카탈로그|\(head\)|\(applied\)|중복 백필|신호 충돌|해석 경로|만료 정책|집계 v/,
  ],
  [
    "어색한 문장",
    /잠시 쉬고|서로 지워|판단을 방해|숫자에서 멈추지|점유율은 기다리고|문체 작성은/,
  ],
  ["방향 단어와 겹치는 부호", /points\([^)]*\)\} (?:올랐|내렸)습니다/],
  ["문장 안 영문 거래소 이름", /거래금액 중 \$\{exchangeLabel\(/],
  [
    "Unix 화면 내부 용어",
    /백엔드 1초|WebSocket 실제 체결|누락 프레임|KRW\/s|경쟁사/,
  ],
];

test("keeps corrected explanation copy plain and consistent", () => {
  for (const { path, source } of displaySources) {
    for (const [reason, pattern] of awkwardCopy) {
      assert.doesNotMatch(source, pattern, `${reason}: ${path}`);
    }
  }
});

test("keeps explicit chart text at least 13 pixels", () => {
  for (const { path, source } of uiSources) {
    assert.doesNotMatch(
      source,
      /fontSize\s*[=:]\s*\{?\s*(?:[0-9]|1[0-2])(?!\d)\s*\}?/,
      `13px 미만의 고정 글자 크기: ${path}`
    );
  }
});

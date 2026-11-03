import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';

test('question page examples use Bithumb regardless of a saved exchange', () => {
  const page = readFileSync(new URL('../pages/MarketShareLLM.tsx', import.meta.url), 'utf8');
  const form = readFileSync(new URL('../components/HermesMarketQuestion.tsx', import.meta.url), 'utf8');
  assert.match(page, /selectedExchange="bithumb"/);
  assert.doesNotMatch(page, /useTargetExchange/);
  assert.match(form, /placeholder="예: 최근 1시간 빗썸 점유율과 거래금액은 왜 변했나요\?"/);
  const quick = form.slice(form.indexOf('const QUICK_QUESTIONS'), form.indexOf('function answerTimestamp'));
  assert.doesNotMatch(quick, /업비트|코인원|코빗|고팍스/);
});

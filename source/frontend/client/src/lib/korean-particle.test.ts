import assert from "node:assert/strict";
import test from "node:test";

import { withParticle } from "./korean-particle.ts";

test("window labels take the particle their last syllable needs", () => {
  // "10초과 직전 구간" read as "10초 초과"; "10초을" was ungrammatical.
  assert.equal(withParticle("10초", "과/와"), "10초와");
  assert.equal(withParticle("30초", "을/를"), "30초를");
  assert.equal(withParticle("1분", "과/와"), "1분과");
  assert.equal(withParticle("1시간", "을/를"), "1시간을");
  assert.equal(withParticle("24시간", "은/는"), "24시간은");
  assert.equal(withParticle("1일", "으로/로"), "1일로");
});

test("amounts are read with their unit or their last digit", () => {
  assert.equal(withParticle("₩94.69억", "으로/로"), "₩94.69억으로");
  assert.equal(withParticle("₩508.1만", "으로/로"), "₩508.1만으로");
  assert.equal(withParticle("₩2.87조", "으로/로"), "₩2.87조로");
  assert.equal(withParticle("₩0", "으로/로"), "₩0으로");
  assert.equal(withParticle("₩7", "으로/로"), "₩7로");
  assert.equal(withParticle("27.26%", "이/가"), "27.26%가");
});

test("tickers are read by their letter names", () => {
  assert.equal(withParticle("BTC", "과/와"), "BTC와");
  assert.equal(withParticle("XRP", "이/가"), "XRP가");
  assert.equal(withParticle("SOL", "이/가"), "SOL이");
  assert.equal(withParticle("SOL", "으로/로"), "SOL로");
  assert.equal(withParticle("ATOM", "은/는"), "ATOM은");
});

test("an unreadable ending keeps the consonant form", () => {
  assert.equal(withParticle("—", "을/를"), "—을");
});

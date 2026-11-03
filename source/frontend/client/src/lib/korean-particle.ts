/**
 * Attaches the particle form that a word takes when read aloud.
 *
 * A fixed particle after a variable reads wrongly for half the values:
 * "10초과 직전 구간" was read as "10초 초과", and "10초을" is ungrammatical.
 * The form depends on whether the last spoken syllable ends in a consonant,
 * and for 으로/로 on whether that consonant is ㄹ.
 */

export type ParticlePair = "과/와" | "은/는" | "이/가" | "을/를" | "으로/로";

type FinalSound = "none" | "rieul" | "other";

// A trailing digit is read as its Korean number: 영 일 이 삼 사 오 육 칠 팔 구.
const DIGIT_FINAL: Record<string, FinalSound> = {
  "0": "other",
  "1": "rieul",
  "2": "none",
  "3": "other",
  "4": "none",
  "5": "none",
  "6": "other",
  "7": "rieul",
  "8": "rieul",
  "9": "none",
};

function finalSound(word: string): FinalSound | null {
  for (let index = word.length - 1; index >= 0; index--) {
    const character = word[index];
    const code = character.charCodeAt(0);
    if (code >= 0xac00 && code <= 0xd7a3) {
      const final = (code - 0xac00) % 28;
      if (final === 0) return "none";
      return final === 8 ? "rieul" : "other";
    }
    if (character in DIGIT_FINAL) return DIGIT_FINAL[character];
    // A ticker is read letter by letter: 엘·알 end in ㄹ, 엠·엔 in another
    // consonant, every other letter name in a vowel (비티씨, 엑스알피).
    if (/[A-Za-z]/.test(character)) {
      const letter = character.toUpperCase();
      if (letter === "L" || letter === "R") return "rieul";
      return letter === "M" || letter === "N" ? "other" : "none";
    }
    // "%" is read 퍼센트; closing marks and spaces are not read.
    if (character === "%") return "none";
    if (/[\s)\]’'"」』.]/.test(character)) continue;
    return null;
  }
  return null;
}

/** The word followed by the particle form it takes, e.g. 10초 → "10초와". */
export function withParticle(word: string, pair: ParticlePair): string {
  const [afterConsonant, afterVowel] = pair.split("/");
  const sound = finalSound(word);
  if (sound === null) return `${word}${afterConsonant}`;
  if (pair === "으로/로") {
    return `${word}${sound === "other" ? afterConsonant : afterVowel}`;
  }
  return `${word}${sound === "none" ? afterVowel : afterConsonant}`;
}

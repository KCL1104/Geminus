// Generates testdata/hashing/v8-number-vectors.txt: IEEE-754 bit patterns and
// their ECMAScript JSON serialization, which RFC 8785 §3.2.2.3 adopts (it
// names V8 as a reference). The Go JCS tests compare against these lines, so
// the vectors come from an independent implementation, not from the code
// under test.
//
//   node scripts/gen_number_vectors.mjs [count] > testdata/hashing/v8-number-vectors.txt
//
// The generator is deterministic (SplitMix64, seed below): the same Node major
// version reproduces the file byte for byte.

const count = Number(process.argv[2] ?? 6000);
const MASK = (1n << 64n) - 1n;
let state = 0x6a09e667f3bcc908n; // fixed seed: first 64 bits of SHA-512's IV

function next() {
  state = (state + 0x9e3779b97f4a7c15n) & MASK;
  let z = state;
  z = ((z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n) & MASK;
  z = ((z ^ (z >> 27n)) * 0x94d049bb133111ebn) & MASK;
  return z ^ (z >> 31n);
}

const view = new DataView(new ArrayBuffer(8));
const fromBits = (bits) => {
  view.setBigUint64(0, bits);
  return view.getFloat64(0);
};
const toBits = (x) => {
  view.setFloat64(0, x);
  return view.getBigUint64(0);
};
const hex = (bits) => bits.toString(16).padStart(16, "0");

const out = [];
const seen = new Set();
function emit(x) {
  if (!Number.isFinite(x)) return;
  const bits = toBits(x);
  const key = hex(bits);
  if (seen.has(key)) return;
  seen.add(key);
  out.push(`${key},${JSON.stringify(x)}`);
}

// Edges: zeros, subnormals, extremes, integer limits and the 1e-6 / 1e21
// boundaries where ECMAScript switches between fixed and exponent notation.
for (const x of [0, -0, Number.MIN_VALUE, -Number.MIN_VALUE, Number.MAX_VALUE, -Number.MAX_VALUE,
  2 ** -1022, 2 ** -1022 - Number.MIN_VALUE, Number.MAX_SAFE_INTEGER, 2 ** 53, 2 ** 53 + 2, 2 ** 68,
  1e21, 1e-6, 1e-7, 0.1, 0.2, 0.1 + 0.2, 1 / 3, 2 / 3, 5e-324, 1e23, 333333333.33333329, 4.5, 0.002]) {
  emit(x);
}
for (const base of [1e-7, 1e-6, 1e20, 1e21, 1e22, 2 ** 53, 1]) {
  const b = toBits(base);
  for (let d = -8n; d <= 8n; d++) emit(fromBits(b + d));
}
for (let e = -324; e <= 308; e++) emit(Number(`1e${e}`));

// Random values from several shapes, so both notations are well covered.
while (out.length < count) {
  const r = next();
  switch (Number(r & 3n)) {
    case 0: // any finite bit pattern
      emit(fromBits(next()));
      break;
    case 1: // integers up to 2^63
      emit(Number(next() >> BigInt(Number(r >> 8n) % 64)));
      break;
    case 2: // short decimals like 4.50 or 120.25
      emit(Number(`${Number(next() % 1000000n)}.${Number(next() % 1000n)}`) * (r & 4n ? -1 : 1));
      break;
    default: // uniform in [0,1) scaled by a power of ten within fixed notation
      emit((Number(next() >> 11n) / 2 ** 53) * 10 ** ((Number(r >> 8n) % 28) - 7));
  }
}
process.stdout.write(`# node ${process.version}; ieee754 hex,JSON.stringify\n${out.join("\n")}\n`);

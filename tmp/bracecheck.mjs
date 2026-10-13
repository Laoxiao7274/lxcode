import fs from 'node:fs';
const src = fs.readFileSync(process.argv[2], 'utf8');
let i = 0, line = 1, depth = 0;
const stack = [];
const errAt = (msg) => { console.log(`${msg} at line ${line} near: ${JSON.stringify(src.slice(i, i + 50))}`); process.exit(1); };
const BS = String.fromCharCode(92);
const TICK = String.fromCharCode(96);
while (i < src.length) {
  const c = src[i];
  if (c === '\n') { line++; i++; continue; }
  if (c === '/' && src[i + 1] === '/') { while (i < src.length && src[i] !== '\n') i++; continue; }
  if (c === '"') { i++; while (i < src.length) { if (src[i] === BS) { i += 2; continue; } if (src[i] === '\n') { line++; errAt('newline in string'); } if (src[i] === '"') break; i++; } i++; continue; }
  if (c === TICK) { i++; while (i < src.length && src[i] !== TICK) { if (src[i] === '\n') line++; i++; } if (src[i] !== TICK) errAt('unterminated raw string'); i++; continue; }
  if (c === '{') { stack.push(line); depth++; i++; continue; }
  if (c === '}') { depth--; stack.pop(); if (depth < 0) errAt('negative depth'); i++; continue; }
  i++;
}
console.log('final depth', depth, 'unclosed at lines:', stack.slice(-10));

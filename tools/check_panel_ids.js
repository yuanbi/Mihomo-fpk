// 校验面板前端：app.js 里取用的元素 id 必须都存在于 index.html。
// 用法：node tools/check_panel_ids.js
const fs = require('fs');
const path = require('path');

const root = path.join(__dirname, '..');
const js = fs.readFileSync(path.join(root, 'app/panel/app.js'), 'utf8');
const html = fs.readFileSync(path.join(root, 'app/panel/index.html'), 'utf8');

const ids = new Set([...html.matchAll(/id="([^"]+)"/g)].map((m) => m[1]));
const used = new Set([...js.matchAll(/\$\('([^']+)'\)/g)].map((m) => m[1]));

const missing = [...used].filter((u) => !ids.has(u));
const unused = [...ids].filter((i) => !used.has(i));

console.log('index.html 中的 id：', ids.size, '个');
console.log('app.js 引用的 id：', used.size, '个');

// 单选组：JS 用 querySelector 按 name 取，必须是 HTML 里真实存在的组。
// 组名从 bindSourceSeg('组名', ...) 调用里取，不依赖拼接模板。
const radioGroups = [...new Set([...js.matchAll(/bindSourceSeg\('([^']+)'/g)].map((m) => m[1]))];
const missingGroups = radioGroups.filter((g) => !html.includes(`name="${g}"`));
console.log('单选组：', radioGroups.join(', ') || '(无)');

let bad = 0;
if (missing.length) { console.log('✗ 缺少的元素 id：', missing.join(', ')); bad++; }
else { console.log('✓ 所有引用的 id 都存在'); }

if (missingGroups.length) { console.log('✗ 缺少的单选组：', missingGroups.join(', ')); bad++; }
else { console.log('✓ 所有单选组都存在'); }

if (unused.length) { console.log('· 未被 JS 引用的 id（仅提示）：', unused.join(', ')); }

process.exit(bad ? 1 : 0);

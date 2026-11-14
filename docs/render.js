// Regenerates the README animations from their source.
//
//   npm install --no-save puppeteer-core        (needs Chrome and ffmpeg)
//   node docs/render.js                         how-it-works.gif, light and dark
//   JUDGE_URL=... JUDGE_KEY=... node docs/render.js playground
//                                               playground.gif, recorded live
//
// how-it-works.html is the animation itself: open it in a browser to watch
// it, edit the script at the top of the file to change it. The playground
// recording drives the real page with a one-hour token that it revokes
// afterwards.
const puppeteer = require('puppeteer-core');
const { execFileSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const CHROME = process.env.CHROME || [
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser',
].find(p => fs.existsSync(p));
const DOCS = __dirname;
const sleep = ms => new Promise(r => setTimeout(r, ms));
const PALETTE = 'split[a][b];[a]palettegen=max_colors=128:stats_mode=full[p];[b][p]paletteuse=dither=none:diff_mode=rectangle';

function gif(inputArgs, filter, out) {
  execFileSync('ffmpeg', ['-v', 'error', '-y', ...inputArgs, '-vf', filter, '-loop', '0', out]);
  console.log(`${path.basename(out)}: ${(fs.statSync(out).size / 1024).toFixed(0)} KB`);
}
const launch = () => puppeteer.launch({ executablePath: CHROME, headless: true, args: ['--hide-scrollbars', '--force-color-profile=srgb'] });

// Frame by frame: the page exposes draw(t) and DURATION.
async function howItWorks(theme) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'judge-anim-'));
  const browser = await launch();
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 960, height: 540, deviceScaleFactor: 1 });
    await page.goto('file://' + path.join(DOCS, 'how-it-works.html') + '?t=0&theme=' + theme);
    const fps = 12.5, n = Math.round((await page.evaluate(() => window.DURATION)) * fps);
    for (let i = 0; i < n; i++) {
      await page.evaluate(t => window.draw(t), i / fps);
      await page.screenshot({ path: path.join(dir, `f${String(i).padStart(4, '0')}.png`) });
    }
    gif(['-framerate', String(fps), '-i', path.join(dir, 'f%04d.png')], PALETTE,
      path.join(DOCS, theme === 'dark' ? 'how-it-works-dark.gif' : 'how-it-works.gif'));
  } finally {
    await browser.close();
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// In real time: run Python, C++ and PostgreSQL, then every language.
async function playground(theme) {
  const url = process.env.JUDGE_URL, key = process.env.JUDGE_KEY;
  if (!url || !key) throw new Error('set JUDGE_URL and JUDGE_KEY');
  const made = await fetch(url + '/admin/tokens', {
    method: 'POST', headers: { 'X-Auth-Token': key, 'Content-Type': 'application/json' },
    body: JSON.stringify({ label: 'demo', ttl_hours: 1, max_submissions: 200 }),
  });
  if (made.status !== 201) throw new Error('could not create a token: HTTP ' + made.status);
  const { token, id } = await made.json();

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'judge-demo-'));
  const browser = await launch();
  try {
    const page = await browser.newPage();
    await page.setBypassCSP(true); // the pointer below is injected markup
    await page.setViewport({ width: 960, height: 700, deviceScaleFactor: 1 });
    await page.emulateMediaFeatures([{ name: 'prefers-color-scheme', value: theme }]);
    await page.goto(url + '/ui/#token=' + encodeURIComponent(token), { waitUntil: 'networkidle0' });
    await page.waitForSelector('#app:not([hidden])');
    const idle = sel => page.waitForFunction(s => !document.querySelector(s).disabled, { timeout: 120000 }, sel);

    // Warm every runner first, so the recording shows steady-state times.
    await page.click('#runall'); await sleep(300); await idle('#runall');
    await page.evaluate(() => localStorage.removeItem('judge.lang'));
    await page.reload({ waitUntil: 'networkidle0' });
    await page.waitForSelector('#app:not([hidden])');

    // A pointer the screenshots can see.
    await page.evaluate(() => {
      const c = document.createElement('div');
      c.id = 'cur';
      c.innerHTML = '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2 L3 17 L7.2 13.2 L10 19.5 L12.6 18.3 L9.8 12.2 L15.5 12.2 Z" fill="#111" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>';
      Object.assign(c.style, { position: 'fixed', left: '0', top: '0', zIndex: '99999', pointerEvents: 'none',
        transform: 'translate(700px, 420px)', transition: 'transform 0.55s cubic-bezier(.4,0,.2,1)' });
      const ring = document.createElement('div');
      ring.id = 'ring';
      Object.assign(ring.style, { position: 'fixed', left: '0', top: '0', width: '28px', height: '28px', borderRadius: '50%',
        border: '2px solid #2a78d6', zIndex: '99998', pointerEvents: 'none', opacity: '0' });
      document.body.append(c, ring);
    });
    const moveTo = async sel => {
      await page.evaluate(s => {
        const r = document.querySelector(s).getBoundingClientRect();
        const x = r.left + Math.min(r.width / 2, 60), y = r.top + r.height / 2;
        document.getElementById('cur').style.transform = `translate(${x}px, ${y}px)`;
        document.getElementById('ring').style.transform = `translate(${x - 12}px, ${y - 12}px)`;
      }, sel);
      await sleep(650);
    };
    const press = async sel => {
      await moveTo(sel);
      await page.evaluate(() => { document.getElementById('ring').style.opacity = '1'; });
      await sleep(160);
      await page.evaluate(s => { document.getElementById('ring').style.opacity = '0'; document.querySelector(s).click(); }, sel);
    };
    const choose = async label => {
      await moveTo('#lang');
      await page.evaluate(l => {
        const sel = document.getElementById('lang');
        sel.value = [...sel.options].find(o => o.textContent.startsWith(l)).value;
        sel.dispatchEvent(new Event('change'));
      }, label);
      await sleep(1100);
    };
    const runOnce = async () => { await press('#run'); await sleep(150); await idle('#run'); await sleep(2000); };
    const scrollToAll = offset => page.evaluate(o => {
      const y = document.getElementById('allcard').getBoundingClientRect().top + window.scrollY - o;
      window.scrollTo({ top: y, behavior: 'smooth' });
    }, offset);

    const frames = []; let recording = true;
    const recorder = (async () => {
      for (let i = 0; recording; i++) {
        const t = Date.now(), file = `f${String(i).padStart(4, '0')}.png`;
        await page.screenshot({ path: path.join(dir, file) });
        frames.push({ t, file });
        await sleep(Math.max(0, 80 - (Date.now() - t)));
      }
    })();

    await sleep(1200);
    await runOnce();
    await choose('C++'); await runOnce();
    await choose('PostgreSQL'); await runOnce();
    await scrollToAll(70); await sleep(900);
    await press('#runall'); await sleep(250);
    await scrollToAll(14);
    await page.evaluate(() => { document.getElementById('cur').style.transform = 'translate(820px, 300px)'; });
    await idle('#runall'); await sleep(3200);
    recording = false; await recorder;

    const list = frames.map((f, k) => `file '${f.file}'\nduration ${(((frames[k + 1] || { t: f.t + 80 }).t - f.t) / 1000).toFixed(3)}`);
    fs.writeFileSync(path.join(dir, 'list.txt'), list.join('\n') + `\nfile '${frames[frames.length - 1].file}'\n`);
    gif(['-f', 'concat', '-safe', '0', '-i', path.join(dir, 'list.txt')], 'fps=10,' + PALETTE,
      path.join(DOCS, theme === 'dark' ? 'playground-dark.gif' : 'playground.gif'));
  } finally {
    await browser.close();
    fs.rmSync(dir, { recursive: true, force: true });
    await fetch(url + '/admin/tokens/' + id, { method: 'DELETE', headers: { 'X-Auth-Token': key } });
  }
}

(async () => {
  if (!CHROME) throw new Error('Chrome not found: set CHROME to its path');
  const job = process.argv[2] === 'playground' ? playground : howItWorks;
  for (const theme of ['light', 'dark']) await job(theme);
})().catch(e => { console.error(String(e.message || e)); process.exit(1); });

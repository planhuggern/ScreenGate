// Optional real-browser integration test. Requires Node 22+ and Playwright.
// Uses only a fresh database under .artifacts; never starts the Windows client.
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { randomBytes } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { DatabaseSync } = require('node:sqlite');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

async function main() {
  const root = path.resolve(__dirname, '..');
  const artifacts = path.join(root, '.artifacts');
  fs.mkdirSync(artifacts, { recursive: true });
  const dbPath = path.join(artifacts, `browser-${Date.now()}.db`);
  const port = Number(process.env.SCREENGATE_TEST_PORT || 18937);
  const origin = `http://127.0.0.1:${port}`;
  const password = randomBytes(24).toString('hex');
  const binary = process.env.SCREENGATE_TEST_BINARY || path.join(artifacts, process.platform === 'win32' ? 'screengate.exe' : 'screengate');
  const server = spawn(binary, [], { cwd: root, windowsHide: true, env: { ...process.env, DATABASE_PATH: dbPath, ADMIN_PASSWORD: password, ADMIN_USER: 'admin', ADMIN_PATH: '/admin', LISTEN_ADDR: `127.0.0.1:${port}`, SCREENGATE_TIMEZONE: 'Europe/Oslo' }, stdio: ['ignore', 'pipe', 'pipe'] });
  let logs = '';
  server.stderr.on('data', chunk => { logs += chunk.toString(); });
  let browser;
  try {
    for (let tries = 0; ; tries++) {
      try { if ((await fetch(`${origin}/healthz`)).ok) break; } catch {}
      if (tries > 100 || server.exitCode !== null) throw new Error(`Server did not start: ${logs}`);
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, ...(process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {}) });
    const publicContext = await browser.newContext();
    assert.equal((await publicContext.request.get(`${origin}/admin`)).status(), 401);
    const admin = await browser.newContext({ httpCredentials: { username: 'admin', password }, viewport: { width: 1440, height: 1100 } });
    const page = await admin.newPage();
    const failures = [];
    page.on('pageerror', error => failures.push(error.message));
    page.on('console', message => { if (message.type() === 'error') failures.push(message.text()); });
    await page.goto(`${origin}/admin`);
    await page.screenshot({ path: path.join(artifacts, 'dashboard-empty.png'), fullPage: true });
    await page.locator('.pair-form input[name=user]').fill('Nora');
    await page.locator('.pair-form button').click();
    const code = (await page.locator('.pair-code').textContent()).trim();
    assert.match(code, /^[A-Z2-7]{4}(?:-[A-Z2-7]{4}){3}$/);
    const enrollment = await fetch(`${origin}/enroll`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code, device_id: 'Nora-PC', user: 'Windows\\Nora' }) });
    assert.equal(enrollment.status, 200);
    const device = await enrollment.json();
    assert.equal(device.user, 'Nora');
    const heartbeat = async () => {
      const res = await fetch(`${origin}/heartbeat`, { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${device.token}` }, body: JSON.stringify({ user: 'Nora', device_id: 'Nora-PC', heartbeat_id: randomBytes(16).toString('hex'), active_seconds: 0, session_state: 'active' }) });
      assert.equal(res.status, 200);
      return res.json();
    };
    assert.equal((await heartbeat()).action, 'allow');
    // The database is unique to this test. Seed varied history for visual review.
    const db = new DatabaseSync(dbPath);
    db.exec('PRAGMA busy_timeout=5000');
    const now = new Date();
    const fixtureUsers = [ ['Nora', 5400, [1800, 3300, 2800, 4200, 2400, 3000, 2340]], ['Emil', 7200, [4200, 3000, 2400, 5100, 4600, 5400, 4260]], ['Signe', 3600, [1200, 1800, 1600, 900, 1500, 1200, 900]] ];
    for (const [user, quota, history] of fixtureUsers) {
      db.prepare('INSERT INTO user_quotas(user,daily_quota_seconds) VALUES(?,?) ON CONFLICT(user) DO UPDATE SET daily_quota_seconds=excluded.daily_quota_seconds').run(user, quota);
      for (let i = 0; i < 7; i++) {
        const at = new Date(now.getTime() - (6 - i) * 86400000);
        db.prepare('INSERT INTO heartbeats(reported_at,reported_unix,accounting_unix,device_id,user,heartbeat_id,credited_milliseconds,credited_until,session_state) VALUES(?,?,?,?,?,?,?,?,?)').run(at.toISOString(), Math.floor(at.getTime()/1000), Math.floor(at.getTime()/1000), `${user}-PC`, user, `fixture-${user}-${i}`, history[i]*1000, at.toISOString(), 'active');
      }
    }
    db.prepare('INSERT INTO user_settings(user,paused) VALUES(?,1)').run('Signe');
    db.close();
    await page.goto(`${origin}/admin`);
    assert.equal(await page.locator('.user-card').count(), 3);
    const nora = page.locator('.user-card').filter({ has: page.getByRole('heading', { name: 'Nora', exact: true }) });
    await nora.getByRole('button', { name: 'Sett på pause' }).click();
    assert.equal((await heartbeat()).reason, 'paused');
    await nora.getByRole('button', { name: 'Åpne skjermtilgang' }).click();
    assert.equal((await heartbeat()).action, 'allow');
    await nora.getByRole('button', { name: 'Gi Nora 15 minutter ekstra i dag' }).click();
    assert.equal((await heartbeat()).quota_seconds, 6300);
    const csv = await admin.request.get(`${origin}/admin/export.csv`);
    assert.equal(csv.status(), 200);
    assert.equal((await csv.text()).trim().split('\n').length, 22);
    await page.screenshot({ path: path.join(artifacts, 'dashboard-desktop.png'), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'mobile dashboard overflows');
    await page.screenshot({ path: path.join(artifacts, 'dashboard-mobile.png'), fullPage: true });
    await nora.locator('.rule-editor > summary').click();
    await nora.locator('.schedule-details > summary').click();
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'expanded mobile schedule overflows');
    await page.screenshot({ path: path.join(artifacts, 'dashboard-schedule-mobile.png'), fullPage: true });
    const dayName = new Intl.DateTimeFormat('en-US', { timeZone: 'Europe/Oslo', weekday: 'short' }).format(new Date());
    const day = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'].indexOf(dayName);
    await nora.locator(`[name=day_${day}_enabled]`).check();
    await nora.locator(`[name=day_${day}_disabled]`).check();
    await nora.getByRole('button', { name: 'Lagre ukeplan' }).click();
    assert.equal((await heartbeat()).reason, 'schedule');
    await nora.locator('.rule-editor > summary').click();
    await nora.locator('.schedule-details > summary').click();
    await nora.locator(`[name=day_${day}_enabled]`).uncheck();
    await nora.getByRole('button', { name: 'Lagre ukeplan' }).click();
    assert.equal((await heartbeat()).action, 'allow');
    const backup = await admin.request.get(`${origin}/admin/backup`);
    assert.equal(backup.status(), 200);
    assert.equal((await backup.body()).subarray(0, 15).toString(), 'SQLite format 3');
    const publicPage = await publicContext.newPage();
    await publicPage.setViewportSize({ width: 1440, height: 1000 });
    await publicPage.goto(origin);
    assert.ok(!(await publicPage.locator('body').innerText()).includes('Nora'), 'public page exposes private data');
    await publicPage.screenshot({ path: path.join(artifacts, 'landing-desktop.png'), fullPage: true });
    assert.deepEqual(failures, []);
    console.log('Browser smoke passed: authentication, enrollment, pause/resume, bonus, weekly rules, CSV, backup, privacy, desktop and mobile layout.');
    console.log(`Screenshots: ${artifacts}`);
  } finally {
    if (browser) await browser.close();
    server.kill();
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });

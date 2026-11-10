import path from 'path';
import fs from 'fs';
import os from 'os';
import { fileURLToPath } from 'url';
import { execSync, execFileSync } from 'child_process';
import { createRequire } from 'module';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const require = createRequire(import.meta.url);

function resolvePlaywright(repoRoot) {
  const candidates = [
    'playwright',
    '@playwright/test',
    'playwright-core',
    path.resolve(repoRoot, '../node_modules/playwright'),
    path.resolve(repoRoot, '../node_modules/@playwright/test'),
    path.resolve(repoRoot, '../node_modules/playwright-core'),
    path.resolve(repoRoot, 'node_modules/playwright'),
  ];
  for (const c of candidates) {
    try {
      const mod = require(c);
      if (mod.chromium) return mod.chromium;
    } catch {}
  }
  throw new Error('Playwright not found in repo or portfolio root. Run npm install at portfolio root.');
}

function resolveFfmpeg(repoRoot) {
  if (process.env.FFMPEG_PATH && fs.existsSync(process.env.FFMPEG_PATH)) {
    return process.env.FFMPEG_PATH;
  }
  try {
    const sys = execSync('which ffmpeg', { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
    if (sys && fs.existsSync(sys)) return sys;
  } catch {}

  const standardPaths = [
    path.resolve(repoRoot, '../node_modules/ffmpeg-static/ffmpeg'),
    path.resolve(repoRoot, 'node_modules/ffmpeg-static/ffmpeg'),
    '/opt/homebrew/bin/ffmpeg',
    '/usr/local/bin/ffmpeg',
  ];
  for (const p of standardPaths) {
    if (fs.existsSync(p)) return p;
  }

  try {
    const ffmpegStatic = require('ffmpeg-static');
    if (ffmpegStatic && fs.existsSync(ffmpegStatic)) return ffmpegStatic;
  } catch {}

  const cacheDir = path.join(os.homedir(), 'Library/Caches/ms-playwright');
  if (fs.existsSync(cacheDir)) {
    for (const entry of fs.readdirSync(cacheDir)) {
      if (entry.startsWith('ffmpeg-')) {
        const bin = path.join(cacheDir, entry, 'ffmpeg-mac');
        if (fs.existsSync(bin)) return bin;
      }
    }
  }
  return null;
}

async function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

(async () => {
  const repoRoot = path.resolve(__dirname, '..');
  const chromium = resolvePlaywright(repoRoot);
  const tempVideoDir = path.join(repoRoot, '.temp_demo_videos');
  if (!fs.existsSync(tempVideoDir)) fs.mkdirSync(tempVideoDir, { recursive: true });

  const assetsDir = path.join(repoRoot, 'docs/assets');
  if (!fs.existsSync(assetsDir)) fs.mkdirSync(assetsDir, { recursive: true });

  const destMp4 = path.join(assetsDir, 'genesis-demo.mp4');
  const destGif = path.join(assetsDir, 'genesis-demo.gif');

  const activeUrl = process.env.GENESIS_HTTP_URL || 'http://127.0.0.1:8080';

  console.log(`🎬 Launching Playwright browser for Genesis demo against ${activeUrl}...`);

  const browser = await chromium.launch({
    headless: true,
    args: ['--no-sandbox', '--disable-gpu', '--window-size=1280,800'],
  });

  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    recordVideo: {
      dir: tempVideoDir,
      size: { width: 1280, height: 800 },
    },
  });

  let latestVideo = null;

  try {
    const page = await context.newPage();

    console.log(`Step 1: Navigating to Genesis UI (${activeUrl})...`);
    await page.goto(activeUrl, { waitUntil: 'domcontentloaded' });
    await sleep(1500);

    console.log('Step 2: Loading aggregated telemetry flood and observing live stream...');
    await page.click('#btn-refresh');
    try {
      await page.waitForSelector('.log-row', { timeout: 5000 });
    } catch (e) {
      console.warn('Timed out waiting for .log-row, continuing anyway...');
    }
    await sleep(3000);

    console.log('Step 3: Finding the Needle in the Haystack (Grep for leaked token)...');
    const grepInput = page.locator('#filter-grep');
    await grepInput.click();
    await grepInput.pressSequentially('token exposed', { delay: 50 });
    await sleep(300);
    await page.click('#btn-refresh');
    await sleep(2500);

    console.log('Step 4: Filtering by Severity (FATAL events)...');
    await grepInput.clear();
    await page.selectOption('#filter-level', 'FATAL');
    await page.click('#btn-refresh');
    await sleep(2500);

    console.log('Step 5: Toggling Theme (Dark -> Light -> Dark)...');
    await page.click('#theme-btn');
    await sleep(1200);
    await page.click('#theme-btn');
    await sleep(1000);

    console.log('Step 6: Resetting filters to observe full aggregated flood...');
    await page.selectOption('#filter-level', '');
    await page.click('#btn-refresh');
    await sleep(2000);

    console.log('Step 7: Performing Amnesiac Purge (Burn All)...');
    await page.click('#open-burn-modal');
    await sleep(1500);
    await page.click('#confirm-burn');
    console.log('✓ Amnesiac Purge confirmed. Zero traces in RAM or disk.');
    await sleep(2500);

    console.log('Closing browser context to finalize video recording...');
    const video = page.video();
    await page.close();
    await context.close();
    await browser.close();

    if (video) {
      try {
        latestVideo = await video.path();
      } catch (e) {
        console.warn('video.path() wait failed:', e.message);
      }
    }
  } catch (err) {
    console.error('❌ Error during browser demo sequence:', err);
    await context.close().catch(() => {});
    await browser.close().catch(() => {});
    fs.rmSync(tempVideoDir, { recursive: true, force: true });
    process.exit(1);
  }

  if (!latestVideo || !fs.existsSync(latestVideo) || fs.statSync(latestVideo).size === 0) {
    const videoFiles = fs.existsSync(tempVideoDir)
      ? fs.readdirSync(tempVideoDir)
          .filter((f) => f.endsWith('.webm'))
          .map((f) => ({ name: f, mtime: fs.statSync(path.join(tempVideoDir, f)).mtimeMs }))
          .sort((a, b) => b.mtime - a.mtime)
      : [];
    if (videoFiles.length === 0) {
      console.error('❌ Error: No recorded WebM video found in temp folder.');
      fs.rmSync(tempVideoDir, { recursive: true, force: true });
      process.exit(1);
    }
    latestVideo = path.join(tempVideoDir, videoFiles[0].name);
  }

  const ffmpegPath = resolveFfmpeg(repoRoot);
  if (!ffmpegPath) {
    console.error('❌ Error: FFmpeg binary not found. Cannot transcode MP4 and GIF.');
    fs.rmSync(tempVideoDir, { recursive: true, force: true });
    process.exit(1);
  }

  try {
    console.log(`🎬 Transcoding recording to web-standard MP4 via ${ffmpegPath}...`);
    execFileSync(ffmpegPath, [
      '-y',
      '-i', latestVideo,
      '-c:v', 'libx264',
      '-pix_fmt', 'yuv420p',
      '-movflags', '+faststart',
      destMp4
    ], { stdio: 'inherit' });
    const stats = fs.statSync(destMp4);
    console.log(`✓ Demo MP4 generated: ${destMp4} (${(stats.size / (1024 * 1024)).toFixed(2)} MB)`);

    console.log('🎬 Transcoding animated preview GIF for GitHub README...');
    execFileSync(ffmpegPath, [
      '-y',
      '-i', destMp4,
      '-vf', 'fps=10,scale=800:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer',
      destGif
    ], { stdio: 'inherit' });
    const gifStats = fs.statSync(destGif);
    console.log(`✓ Demo GIF generated: ${destGif} (${(gifStats.size / (1024 * 1024)).toFixed(2)} MB)`);
  } catch (err) {
    console.error('❌ Error during FFmpeg transcoding:', err.message);
    fs.rmSync(tempVideoDir, { recursive: true, force: true });
    process.exit(1);
  }

  // Clean up temp dir
  fs.rmSync(tempVideoDir, { recursive: true, force: true });
  console.log('✨ Demo video recording complete!');
})();

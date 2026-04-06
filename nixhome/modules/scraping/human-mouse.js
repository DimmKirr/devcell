// human-mouse.js — Realistic human mouse movement for Playwright v2
//
// Paste the helper block below into browser_run_code scripts.
// Tracks mouse position across calls (_mx/_my).
//
// Features:
//   - Cubic Bezier curves with asymmetric control points
//   - Overshoot proportional to distance, then smooth correction
//   - Quintic ease-in-out (slow→fast→slow, more pronounced than smoothstep)
//   - Mid-path micro-corrections on long moves (hesitation/direction change)
//   - Hand tremor inversely proportional to speed
//   - Occasional micro-pauses (2% chance mid-path)
//   - Short moves (<50px) use direct path with jitter (no Bezier overhead)
//   - Pre-click hover pause (30-150ms)
//   - Post-click drift (hand shifts 1-4px after clicking)
//   - Off-center clicks (35-65% of element width/height)
//   - Variable typing speed with word-boundary pauses and rare thinking pauses
//
// ── Copy this block into browser_run_code ──────────────────────────────────

/*
let _mx = 960, _my = 540;

async function hmMove(page, tx, ty) {
  const sx = _mx, sy = _my;
  const dist = Math.hypot(tx-sx, ty-sy);
  if (dist < 2) { _mx=tx; _my=ty; return; }

  if (dist < 50) {
    const steps = 5 + ~~(Math.random()*5);
    for (let i=1; i<=steps; i++) {
      const t = i/steps, e = t*t*(3-2*t);
      await page.mouse.move(
        sx+(tx-sx)*e+(Math.random()-0.5)*2,
        sy+(ty-sy)*e+(Math.random()-0.5)*2
      );
      await page.waitForTimeout(5+Math.random()*10);
    }
    await page.mouse.move(tx, ty); _mx=tx; _my=ty; return;
  }

  const steps = Math.max(30, ~~(dist/5)+~~(Math.random()*20));
  const dur = 200+dist*1.0+Math.random()*250;
  const ang = Math.atan2(ty-sy, tx-sx), perp = ang+Math.PI/2;
  const arcMag = dist*(0.08+Math.random()*0.15)*(Math.random()>0.5?1:-1);
  const cp1t = 0.2+Math.random()*0.15, cp2t = 0.65+Math.random()*0.15;
  const cx1 = sx+(tx-sx)*cp1t+Math.cos(perp)*arcMag;
  const cy1 = sy+(ty-sy)*cp1t+Math.sin(perp)*arcMag;
  const cx2 = sx+(tx-sx)*cp2t+Math.cos(perp)*arcMag*0.6;
  const cy2 = sy+(ty-sy)*cp2t+Math.sin(perp)*arcMag*0.6;
  const ov = 4+(dist/200)*5+Math.random()*4;
  const ox = tx+Math.cos(ang)*ov, oy = ty+Math.sin(ang)*ov;
  const doCorr = dist>200&&Math.random()>0.4;
  const corrT = 0.55+Math.random()*0.15, corrM = (Math.random()-0.5)*dist*0.03;

  for (let i=0; i<=steps; i++) {
    const t = i/steps;
    let e; if (t<0.5) e=16*t*t*t*t*t; else { const f=-2*t+2; e=1-f*f*f*f*f/2; }
    let x, y;
    if (t<0.88) {
      const b=Math.min(e/0.88,1), u=1-b;
      x=u*u*u*sx+3*u*u*b*cx1+3*u*b*b*cx2+b*b*b*ox;
      y=u*u*u*sy+3*u*u*b*cy1+3*u*b*b*cy2+b*b*b*oy;
    } else {
      const c=(t-0.88)/0.12, ce=c*c*(3-2*c);
      x=ox+(tx-ox)*ce; y=oy+(ty-oy)*ce;
    }
    if (doCorr&&Math.abs(t-corrT)<0.03) { x+=corrM; y+=corrM*0.7; }
    const tr = 0.5+(1-Math.sin(t*Math.PI))*1.5;
    x+=(Math.random()-0.5)*tr; y+=(Math.random()-0.5)*tr;
    await page.mouse.move(x, y);
    const spd = 0.3+Math.sin(t*Math.PI)*1.0;
    let dl = (dur/steps)/spd;
    if (Math.random()<0.02&&t>0.2&&t<0.8) dl+=30+Math.random()*60;
    await page.waitForTimeout(dl+Math.random()*3);
  }
  await page.mouse.move(tx, ty); _mx=tx; _my=ty;
}

async function hmClick(page, locator) {
  const b = await locator.boundingBox();
  const x = b.x+b.width*(0.35+Math.random()*0.3);
  const y = b.y+b.height*(0.35+Math.random()*0.3);
  await hmMove(page, x, y);
  await page.waitForTimeout(30+Math.random()*120);
  await page.mouse.click(x, y);
  await page.waitForTimeout(20+Math.random()*40);
  _mx=x+(Math.random()-0.5)*4; _my=y+(Math.random()-0.5)*4;
  await page.mouse.move(_mx, _my);
}

async function hmType(page, text) {
  for (const ch of text) {
    await page.keyboard.type(ch);
    let d = 40+Math.random()*80;
    if (' /-.'.includes(ch)) d+=80+Math.random()*120;
    if (Math.random()<0.04) d+=200+Math.random()*300;
    await page.waitForTimeout(d);
  }
}
*/

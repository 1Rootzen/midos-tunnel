<script setup>
import { onMounted, onBeforeUnmount, ref } from 'vue';

// A moving neural net behind every page: neurons drift, the ones close to each
// other are wired together, and impulses run along the wires and fire on to
// the next neuron. Neurons near the pointer reach out to it. Drawn on one
// canvas; stops while the tab is hidden and stays still for reduced motion.
const cv = ref(null);
const RGB = '108, 117, 230'; // the brand blue, lightened to read on black
const LINK = 170; // px within which two neurons are wired
const REACH = 190; // px within which neurons reach for the pointer

let ctx, w = 0, h = 0, dpr = 1, raf = 0, last = 0, spawnAt = 0;
let nodes = [];
let pulses = [];
let nbrs = [];
const ptr = { x: -1e4, y: -1e4 };

// a soft glow, drawn once and stamped wherever a neuron fires
const glow = document.createElement('canvas');
glow.width = glow.height = 64;
{
  const g = glow.getContext('2d');
  const r = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  r.addColorStop(0, `rgba(${RGB}, 0.9)`);
  r.addColorStop(0.25, `rgba(${RGB}, 0.35)`);
  r.addColorStop(1, `rgba(${RGB}, 0)`);
  g.fillStyle = r;
  g.fillRect(0, 0, 64, 64);
}

function resize() {
  dpr = Math.min(window.devicePixelRatio || 1, 1.5);
  w = window.innerWidth;
  h = window.innerHeight;
  cv.value.width = Math.round(w * dpr);
  cv.value.height = Math.round(h * dpr);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  const want = Math.max(40, Math.min(170, Math.round((w * h) / 8500)));
  while (nodes.length < want) {
    const a = Math.random() * Math.PI * 2;
    const v = 0.06 + Math.random() * 0.12;
    nodes.push({ x: Math.random() * w, y: Math.random() * h, vx: Math.cos(a) * v, vy: Math.sin(a) * v, r: 1.3 + Math.random() * 1.6, fire: 0 });
  }
  nodes.length = want;
  pulses = pulses.filter((p) => p.a < want && p.b < want);
}

function step(dt) {
  for (const n of nodes) {
    n.x += n.vx * dt;
    n.y += n.vy * dt;
    if (n.x < -20) n.x = w + 20; else if (n.x > w + 20) n.x = -20;
    if (n.y < -20) n.y = h + 20; else if (n.y > h + 20) n.y = -20;
    const dx = ptr.x - n.x, dy = ptr.y - n.y, d = Math.hypot(dx, dy);
    if (d < REACH && d > 30) { n.x += (dx / d) * 0.012 * dt; n.y += (dy / d) * 0.012 * dt; }
    n.fire = Math.max(0, n.fire - dt / 700);
  }
}

function draw(now, dt) {
  ctx.clearRect(0, 0, w, h);
  nbrs = nodes.map(() => []);
  ctx.lineWidth = 1;
  for (let i = 0; i < nodes.length; i++) {
    const a = nodes[i];
    for (let j = i + 1; j < nodes.length; j++) {
      const b = nodes[j];
      const dx = a.x - b.x, dy = a.y - b.y;
      if (dx > LINK || dx < -LINK || dy > LINK || dy < -LINK) continue;
      const d = Math.hypot(dx, dy);
      if (d >= LINK) continue;
      nbrs[i].push(j);
      nbrs[j].push(i);
      const k = 1 - d / LINK;
      ctx.strokeStyle = `rgba(${RGB}, ${(k * 0.42 + Math.max(a.fire, b.fire) * k * 0.45).toFixed(3)})`;
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    }
    const dx = ptr.x - a.x, dy = ptr.y - a.y, d = Math.hypot(dx, dy);
    if (d < REACH) {
      ctx.strokeStyle = `rgba(${RGB}, ${((1 - d / REACH) * 0.45).toFixed(3)})`;
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(ptr.x, ptr.y);
      ctx.stroke();
    }
  }

  // impulses: born on a random neuron, run to a neighbour, and often fire on
  if (dt && now > spawnAt && pulses.length < 40) {
    spawnAt = now + 140 + Math.random() * 260;
    const a = (Math.random() * nodes.length) | 0;
    if (nbrs[a].length) pulses.push({ a, b: nbrs[a][(Math.random() * nbrs[a].length) | 0], t: 0, hop: 0 });
  }
  const next = [];
  for (const p of pulses) {
    const A = nodes[p.a], B = nodes[p.b];
    p.t += dt / (520 + Math.hypot(A.x - B.x, A.y - B.y) * 4);
    if (p.t >= 1) {
      B.fire = 1;
      const on = nbrs[p.b].filter((x) => x !== p.a);
      if (on.length && p.hop < 6 && Math.random() < 0.72) next.push({ a: p.b, b: on[(Math.random() * on.length) | 0], t: 0, hop: p.hop + 1 });
      continue;
    }
    if (Math.hypot(A.x - B.x, A.y - B.y) > LINK * 1.15) continue; // the wire broke
    next.push(p);
    const x = A.x + (B.x - A.x) * p.t, y = A.y + (B.y - A.y) * p.t;
    ctx.globalAlpha = 0.85;
    ctx.drawImage(glow, x - 9, y - 9, 18, 18);
    ctx.globalAlpha = 1;
  }
  pulses = next;

  for (const n of nodes) {
    if (n.fire > 0.02) {
      ctx.globalAlpha = n.fire;
      const s = 16 + n.fire * 18;
      ctx.drawImage(glow, n.x - s / 2, n.y - s / 2, s, s);
      ctx.globalAlpha = 1;
    }
    ctx.fillStyle = `rgba(${RGB}, ${(0.7 + n.fire * 0.3).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(n.x, n.y, n.r + n.fire * 1.2, 0, Math.PI * 2);
    ctx.fill();
  }
}

function frame(now) {
  const dt = Math.min(50, last ? now - last : 16);
  last = now;
  step(dt);
  draw(now, dt);
  raf = requestAnimationFrame(frame);
}

const still = window.matchMedia('(prefers-reduced-motion: reduce)');
function start() {
  if (raf || document.hidden || still.matches) return;
  last = 0;
  raf = requestAnimationFrame(frame);
}
function stop() { cancelAnimationFrame(raf); raf = 0; }
const onVis = () => (document.hidden ? stop() : start());
const onMove = (e) => { ptr.x = e.clientX; ptr.y = e.clientY; };
const onLeave = () => { ptr.x = ptr.y = -1e4; };
const onResize = () => { resize(); if (!raf) draw(0, 0); };

onMounted(() => {
  ctx = cv.value.getContext('2d');
  resize();
  draw(0, 0);
  start();
  window.addEventListener('resize', onResize);
  window.addEventListener('pointermove', onMove, { passive: true });
  document.addEventListener('pointerleave', onLeave);
  document.addEventListener('visibilitychange', onVis);
});
onBeforeUnmount(() => {
  stop();
  window.removeEventListener('resize', onResize);
  window.removeEventListener('pointermove', onMove);
  document.removeEventListener('pointerleave', onLeave);
  document.removeEventListener('visibilitychange', onVis);
});
</script>

<template>
  <div class="hud-bg" aria-hidden="true">
    <canvas ref="cv" class="hud-net"></canvas>
  </div>
</template>

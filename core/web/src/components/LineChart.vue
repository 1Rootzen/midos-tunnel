<script setup>
import { ref, watch, onMounted, onBeforeUnmount } from 'vue';

// A small canvas chart: one or more series over the same points, scaled to the
// highest value, drawn at the screen's pixel density. Redraws only when the
// data or the size changes.
const props = defineProps({
  points: { type: Array, default: () => [] },
  series: { type: Array, required: true }, // [{ key, color, glow }]
});
const canvas = ref(null);
let ro;

function draw() {
  const cv = canvas.value;
  if (!cv) return;
  const dpr = window.devicePixelRatio || 1;
  const w = cv.clientWidth, h = cv.clientHeight;
  if (!w) return;
  if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) {
    cv.width = Math.round(w * dpr);
    cv.height = Math.round(h * dpr);
  }
  const g = cv.getContext('2d');
  g.setTransform(dpr, 0, 0, dpr, 0, 0);
  g.clearRect(0, 0, w, h);
  g.strokeStyle = 'rgba(255,255,255,0.06)';
  g.lineWidth = 1;
  for (let i = 1; i < 4; i++) { g.beginPath(); g.moveTo(0, h * i / 4); g.lineTo(w, h * i / 4); g.stroke(); }
  const pts = props.points;
  if (!pts.length) return;
  let max = 1e-9;
  for (const p of pts) for (const s of props.series) max = Math.max(max, p[s.key] || 0);
  const xy = (i, v) => [pts.length < 2 ? w : w * i / (pts.length - 1), h - 6 - (h - 12) * v / max];
  for (const s of props.series) {
    g.beginPath();
    pts.forEach((p, i) => { const [x, y] = xy(i, p[s.key] || 0); if (i) g.lineTo(x, y); else g.moveTo(x, y); });
    if (s.glow) {
      // soft area under the main series
      const grad = g.createLinearGradient(0, 0, 0, h);
      grad.addColorStop(0, s.color + '44');
      grad.addColorStop(1, s.color + '00');
      g.save();
      g.lineTo(w, h); g.lineTo(0, h); g.closePath();
      g.fillStyle = grad; g.fill();
      g.restore();
      g.beginPath();
      pts.forEach((p, i) => { const [x, y] = xy(i, p[s.key] || 0); if (i) g.lineTo(x, y); else g.moveTo(x, y); });
    }
    g.strokeStyle = s.color; g.lineWidth = 2; g.lineJoin = 'round';
    g.shadowColor = s.color; g.shadowBlur = s.glow ? 10 : 0;
    g.stroke();
    g.shadowBlur = 0;
  }
}

watch(() => props.points, draw);
onMounted(() => {
  draw();
  ro = new ResizeObserver(draw);
  ro.observe(canvas.value);
});
onBeforeUnmount(() => ro && ro.disconnect());
</script>

<template>
  <canvas ref="canvas" class="chart"></canvas>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue';
import { api, BASE } from '../lib/api.js';
import { fwd } from '../lib/i18n.js';

// The sponsor strip across the top of the panel. Slides come from the server
// (see panel_promo.go) and rotate on their own timer. It is part of the shell
// and has no close button by design: the project is funded this way. Please
// keep it in place if you redistribute the panel — see LICENSE.

const slides = ref([]);
const interval = ref(9);
const i = ref(0);
let rot, refresh;

async function load() {
  try {
    const d = await api('GET', '/api/promo', undefined, { quiet: true });
    if (d && Array.isArray(d.slides) && d.slides.length) {
      slides.value = d.slides;
      interval.value = Math.max(4, d.interval || 9);
      if (i.value >= slides.value.length) i.value = 0;
    }
  } catch (_) { /* keep whatever is already showing */ }
}

const slide = computed(() => slides.value[i.value] || null);
const imgSrc = computed(() => (slide.value && slide.value.image ? BASE + '/api/promo/image/' + encodeURIComponent(slide.value.id) : ''));
const style = computed(() => (slide.value ? { background: slide.value.bg || '', color: slide.value.fg || '' } : {}));
// "Brand · message": the part before the dot is the headline, in the slide's
// title colour; the rest is the subtitle
const parts = computed(() => {
  const t = (slide.value && slide.value.text) || '';
  const k = t.search(/\s*[·|]\s*/);
  if (k < 0) return { head: t, sub: '' };
  return { head: t.slice(0, k).trim(), sub: t.slice(k).replace(/^\s*[·|]\s*/, '').trim() };
});
const headStyle = computed(() => (slide.value && slide.value.accent ? { color: slide.value.accent } : {}));

function step() { if (slides.value.length) i.value = (i.value + 1) % slides.value.length; }
function go(url) { if (url) window.open(url, '_blank', 'noopener'); }

onMounted(() => {
  load();
  rot = setInterval(step, interval.value * 1000);
  refresh = setInterval(load, 15 * 60 * 1000); // pick up new ads during a long session
});
onBeforeUnmount(() => { clearInterval(rot); clearInterval(refresh); });
</script>

<template>
  <div v-if="slide" class="promo" :class="{ link: !!slide.url, 'has-img': !!imgSrc }" :style="style" :title="slide.url ? slide.cta || slide.text : ''" @click="go(slide.url)">
    <span v-if="imgSrc" class="promo-art" :style="{ backgroundImage: 'url(' + imgSrc + ')' }"></span>
    <span class="promo-tag">{{ $t('حامی') }}</span>
    <span class="promo-text" dir="auto"><b class="promo-head" :style="headStyle">{{ parts.head }}</b><template v-if="parts.sub"><i class="promo-sep"></i><span class="promo-sub">{{ parts.sub }}</span></template></span>
    <span v-if="slide.url" class="promo-cta" dir="auto">{{ slide.cta || $t('مشاهده') }} {{ fwd() }}</span>
    <span v-if="slides.length > 1" class="promo-dots">
      <i v-for="(s, n) in slides" :key="s.id || n" :class="{ on: n === i }"></i>
    </span>
  </div>
</template>

<script>
// Windows share one stacking order: the one last touched is on top, and
// Escape closes only that one.
let topZ = 1000;
const stack = [];
let modals = 0; // open modal windows; the page scrolls again when it is 0
</script>

<script setup>
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue';
import AppIcon from './AppIcon.vue';

const props = defineProps({
  title: { type: String, default: '' },
  icon: { type: String, default: 'window' },
  width: { type: Number, default: 640 },
  closable: { type: Boolean, default: true },
  modal: { type: Boolean, default: true },
  // 'side' opens beside the content (an inspector) instead of in the middle
  place: { type: String, default: 'center' },
});
const emit = defineEmits(['close']);

const root = ref(null);
const x = ref(0);
const y = ref(0);
const z = ref(++topZ);
const dragging = ref(false);
const narrow = () => window.innerWidth < 720;
// the height actually visible: on a phone that excludes the browser's bars and
// the on-screen keyboard, which 100vh does not
const viewH = () => (window.visualViewport ? window.visualViewport.height : window.innerHeight);
const vh = ref(viewH());
const me = {};
let drag = null;

function front() {
  z.value = ++topZ;
  const i = stack.indexOf(me);
  if (i >= 0) stack.splice(i, 1);
  stack.push(me);
}

function clamp(nx, ny) {
  const el = root.value;
  const w = el ? el.offsetWidth : props.width;
  // keep at least the title bar reachable
  return [Math.min(Math.max(nx, 8 - w + 120), window.innerWidth - 120), Math.min(Math.max(ny, 8), vh.value - 56)];
}

function center() {
  const el = root.value;
  if (!el) return;
  const w = el.offsetWidth, h = el.offsetHeight;
  if (narrow()) { x.value = 8; y.value = 8; return; }
  if (props.place === 'side') { x.value = 24; y.value = Math.max(16, Math.min(132, vh.value - h - 16)); return; }
  x.value = Math.max(8, (window.innerWidth - w) / 2);
  y.value = Math.max(16, Math.min((vh.value - h) / 2, vh.value * 0.1));
}

function down(e) {
  front();
  if (e.button !== 0 || e.target.closest('button')) return;
  drag = { sx: e.clientX, sy: e.clientY, x: x.value, y: y.value };
  dragging.value = true;
  try { e.currentTarget.setPointerCapture(e.pointerId); } catch (_) { /* ignore */ }
}
function move(e) {
  if (!drag) return;
  const [nx, ny] = clamp(drag.x + e.clientX - drag.sx, drag.y + e.clientY - drag.sy);
  x.value = nx;
  y.value = ny;
}
function up() { drag = null; dragging.value = false; }

function onKey(e) {
  if (e.key === 'Escape' && props.closable && stack[stack.length - 1] === me) {
    e.stopPropagation();
    emit('close');
  }
}
// a phone resizes the view whenever its bars or keyboard come and go: keep the
// window where it was, only pulled back on screen
function onResize() {
  vh.value = viewH();
  const [nx, ny] = clamp(x.value, y.value);
  x.value = nx; y.value = ny;
}
// the window never reaches below the visible bottom edge; what does not fit
// scrolls inside it
const panelMax = () => Math.max(160, vh.value - Math.max(y.value, 0) - 8) + 'px';

onMounted(async () => {
  stack.push(me);
  if (props.modal && ++modals === 1) document.body.classList.add('modal-open');
  document.addEventListener('keydown', onKey);
  window.addEventListener('resize', onResize);
  if (window.visualViewport) window.visualViewport.addEventListener('resize', onResize);
  await nextTick();
  center();
});
onBeforeUnmount(() => {
  const i = stack.indexOf(me);
  if (i >= 0) stack.splice(i, 1);
  document.removeEventListener('keydown', onKey);
  window.removeEventListener('resize', onResize);
  if (window.visualViewport) window.visualViewport.removeEventListener('resize', onResize);
  if (props.modal && --modals === 0) document.body.classList.remove('modal-open');
});
</script>

<template>
  <Teleport to="body">
    <div v-if="modal" class="win-backdrop" :style="{ zIndex: z - 1 }"></div>
    <div ref="root" class="win" :class="{ dragging }" role="dialog" :aria-modal="modal"
      :style="{ zIndex: z, width: width + 'px', transform: `translate3d(${x}px, ${y}px, 0)` }" @pointerdown="front">
      <div class="win-panel glass" :style="{ maxHeight: panelMax() }">
        <div class="win-head" @pointerdown="down" @pointermove="move" @pointerup="up" @pointercancel="up" @dblclick="center">
          <AppIcon name="grip" class="grip" />
          <h3><AppIcon :name="icon" /><span>{{ title }}</span></h3>
          <slot name="head" />
          <button v-if="closable" type="button" class="btn small icon-only" :title="$t('بستن (Esc)')" @click="emit('close')">
            <AppIcon name="close" />
          </button>
        </div>
        <div class="win-body"><slot /></div>
        <div v-if="$slots.foot" class="win-foot"><slot name="foot" /></div>
      </div>
    </div>
  </Teleport>
</template>

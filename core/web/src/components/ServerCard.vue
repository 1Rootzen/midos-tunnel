<script setup>
import { computed } from 'vue';
import { fa, bytes, mbit, dur } from '../lib/format.js';
import { t, pct as pctSign } from '../lib/i18n.js';
import { ROLE_FA, NODE_STATE, cableState, cableKindText, nodeName, pct } from '../lib/fleet.js';
import { STATE_TEXT, benchGet } from '../topology/model.js';
import { KERNEL_MODES } from '../topology/engine.js';
import AppIcon from './AppIcon.vue';

// One server of the fleet: what it is, how loaded it is right now, how fast
// the benchmark found it, and which servers it is tunnelled with.
const props = defineProps({
  n: { type: Object, required: true },
  byId: { type: Object, required: true },
  links: { type: Array, required: true },
  bench: { type: Object, required: true },
});
const emit = defineEmits(['bench', 'details', 'install', 'progress', 'remove']);

const self = computed(() => props.n.kind === 'self');
const st = computed(() => props.n.stats);
const offline = computed(() => st.value && st.value.error);
const f = computed(() => props.n.facts || {});
const state = computed(() => NODE_STATE[props.n.state || ''] || NODE_STATE['']);
const job = computed(() => props.n.job);
const curStep = computed(() => (job.value && !job.value.done ? (job.value.steps.find((s) => s.state === 'run') || {}).name : ''));
const stepPct = computed(() => (job.value ? Math.round((100 * job.value.steps.filter((s) => s.state !== 'run').length) / 8) : 0));

const R = 15, C = 2 * Math.PI * R;
const rings = computed(() => {
  const s = st.value;
  if (!s || s.error) return [];
  return [
    ['CPU', s.cpu, fa(Math.round(s.cpu)) + pctSign()],
    ['RAM', pct(s.mem_used, s.mem_total), bytes(s.mem_used)],
    ['DISK', pct(s.disk_used, s.disk_total), bytes(s.disk_used)],
  ].map(([k, p, v]) => ({ k, v, p, off: C * (1 - Math.min(100, Math.max(0, p || 0)) / 100) }));
});

const specs = computed(() => {
  const r = props.bench.results[props.n.id];
  const loc = r ? benchGet(r, 'Location') : '';
  const org = r ? benchGet(r, 'Organization') : '';
  return [
    ['سیستم‌عامل', f.value.os], ['کرنل', f.value.kernel],
    ['پردازنده', f.value.cpu ? f.value.cpu + (f.value.cores ? ' · ' + t('{n} هسته', { n: fa(f.value.cores) }) : '') : ''],
    ['حافظه', f.value.mem_total ? bytes(f.value.mem_total) : ''], ['دیسک', f.value.disk_total ? bytes(f.value.disk_total) : ''],
    ['معماری', [f.value.arch, f.value.virt && f.value.virt !== 'none' ? f.value.virt : ''].filter(Boolean).join(' · ')],
    ['دیتاسنتر', org], ['موقعیت', loc], ['midos-tunnel', f.value.veil],
  ].filter(([, v]) => v);
});

const toMbps = (s) => { const m = /([\d.]+)\s*([KMG])bps/.exec(s || ''); return m ? parseFloat(m[1]) * ({ K: 0.001, M: 1, G: 1000 })[m[2]] : 0; };
const benchRun = computed(() => props.bench.running[props.n.id]);
const benchSum = computed(() => {
  const r = props.bench.results[props.n.id];
  if (!r || !r.ok) return r ? { error: r.error || 'ناموفق' } : null;
  const best = (r.speed || []).reduce((a, s) => (toMbps(s.download) > toMbps(a.download || '') ? s : a), {});
  const io = (r.io || []).find((x) => /average/i.test(x.key));
  return { down: best.download, up: best.upload, node: best.node, io: io && io.value, at: r.at };
});

const tunnels = computed(() => props.links
  .filter((l) => l.a === props.n.id || l.b === props.n.id)
  .map((l) => {
    const other = props.byId[l.a === props.n.id ? l.b : l.a] || {};
    const s = cableState(l, props.byId);
    return { id: l.id, peer: nodeName(other), role: ROLE_FA[other.role] || '', kind: cableKindText(l, KERNEL_MODES), st: s, text: STATE_TEXT[s] || s };
  }));
const DOT = { up: 'ok', warn: 'warn', down: 'bad', off: '', draft: '', missing: 'bad' };
</script>

<template>
  <div class="card srv-card" :class="['st-' + (n.state || 'none'), { self, offline }]">
    <div class="srv-head">
      <div class="srv-ic" :class="n.role"><AppIcon :name="self ? 'shield' : 'cpu'" /></div>
      <div class="srv-title">
        <h3>{{ self ? (n.label || $t('سرور مادر')) : nodeName(n) }}</h3>
        <div class="srv-sub"><span class="ltr">{{ n.host || n.ip || '—' }}</span><span v-if="f.hostname && f.hostname !== n.label" class="faint ltr">· {{ f.hostname }}</span></div>
      </div>
      <div class="srv-pills">
        <span v-if="self" class="pill accent">{{ $t('سرور مادر') }}</span>
        <span class="pill" :class="n.role === 'iran' ? 'ok' : (n.role === 'kharej' ? 'info' : '')">{{ $t(ROLE_FA[n.role]) || '—' }}</span>
        <span v-if="!self" class="pill" :class="offline ? 'bad' : state[0]">{{ offline ? $t('آفلاین') : $t(state[1]) }}</span>
      </div>
    </div>

    <!-- installing -->
    <button v-if="n.state === 'installing'" class="srv-installing" @click="emit('progress', n)">
      <div class="srv-progress"><i :style="{ width: stepPct + '%' }"></i></div>
      <span><span class="spin"></span> {{ curStep ? $t(curStep) : $t('در حال نصب…') }}</span><small>{{ $t('مشاهدهٔ مراحل') }}</small>
    </button>
    <div v-else-if="n.state === 'error'" class="hint warn srv-err">
      <AppIcon name="alert" /> {{ $t(n.error) }}
      <div class="actions"><button class="btn small" @click="emit('install', n)"><AppIcon name="redo" /> {{ $t('تلاش دوباره') }}</button>
        <button v-if="job" class="btn small" @click="emit('progress', n)"><AppIcon name="list" /> {{ $t('خروجی') }}</button></div>
    </div>

    <!-- live resources -->
    <div v-if="rings.length" class="srv-res">
      <div v-for="r in rings" :key="r.k" class="srv-ring" :class="{ hot: r.p >= 90 }">
        <svg viewBox="0 0 40 40"><circle cx="20" cy="20" :r="R" class="track" /><circle cx="20" cy="20" :r="R" class="meter" :stroke-dasharray="C" :stroke-dashoffset="r.off" /></svg>
        <div><b class="num">{{ r.k === 'CPU' ? r.v : fa(Math.round(r.p)) + pctSign() }}</b><small>{{ r.k }}{{ r.k !== 'CPU' ? ' · ' + r.v : '' }}</small></div>
      </div>
      <div class="srv-net">
        <div><i class="rx"></i>{{ $t('دریافت') }} <b class="num">{{ mbit(st.rx) }}</b></div>
        <div><i class="tx"></i>{{ $t('ارسال') }} <b class="num">{{ mbit(st.tx) }}</b></div>
        <small>{{ $t('{d} روشن', { d: dur(st.uptime_s) }) }} · load <span class="ltr">{{ st.load }}</span></small>
      </div>
    </div>
    <div v-else-if="offline" class="hint warn">{{ $t(st.error) }}</div>
    <div v-else-if="n.state === 'ready' || self" class="muted srv-wait"><span class="spin"></span> {{ $t('در حال خواندن منابع…') }}</div>

    <!-- specs -->
    <dl v-if="specs.length" class="srv-specs">
      <template v-for="[k, v] in specs" :key="k"><dt>{{ $t(k) }}</dt><dd>{{ v }}</dd></template>
    </dl>

    <!-- benchmark -->
    <div v-if="n.state === 'ready' || self" class="srv-bench">
      <div class="srv-sec"><AppIcon name="gauge" /> {{ $t('بنچمارک') }}
        <span class="srv-sp"></span>
        <button v-if="benchSum && !benchRun" class="btn small" @click="emit('details', n)">{{ $t('جزئیات') }}</button>
        <button class="btn small" :disabled="!!benchRun" @click="emit('bench', n)"><span v-if="benchRun" class="spin"></span><AppIcon v-else name="play" /> {{ $t(benchRun ? 'در حال اجرا…' : (benchSum ? 'دوباره' : 'اجرا')) }}</button>
      </div>
      <div v-if="benchRun" class="faint">{{ $t('bench.sh در حال اجراست؛ ۳ تا ۸ دقیقه…') }}</div>
      <div v-else-if="benchSum && benchSum.error" class="hint warn">{{ $t(benchSum.error) }}</div>
      <div v-else-if="benchSum" class="srv-bench-vals">
        <div v-if="benchSum.down"><small>{{ $t('بهترین دانلود') }}</small><b class="ltr">{{ benchSum.down }}</b></div>
        <div v-if="benchSum.up"><small>{{ $t('آپلود') }}</small><b class="ltr">{{ benchSum.up }}</b></div>
        <div v-if="benchSum.io"><small>{{ $t('I/O دیسک') }}</small><b class="ltr">{{ benchSum.io }}</b></div>
      </div>
      <div v-else class="faint">{{ $t('هنوز اجرا نشده') }}</div>
    </div>

    <!-- tunnels -->
    <div class="srv-tun">
      <div class="srv-sec"><AppIcon name="link" /> {{ $t('تانل‌ها') }} <span class="faint num">{{ fa(tunnels.length) }}</span></div>
      <div v-for="t in tunnels" :key="t.id" class="srv-tun-row">
        <i class="dot" :class="DOT[t.st]"></i>
        <span class="peer">↔ {{ t.peer }}</span><span class="faint">{{ $t(t.role) }}</span>
        <span class="kind">{{ t.kind }}</span>
        <span class="st" :class="'st-' + t.st">{{ $t(t.text) }}</span>
      </div>
      <div v-if="!tunnels.length" class="faint">{{ $t('با سروری تانل نیست') }} — <router-link to="/topology">{{ $t('در طراحی شبکه کابل بکشید') }}</router-link></div>
    </div>

    <div v-if="!self" class="actions srv-actions">
      <button v-if="n.state === 'ready'" class="btn small" @click="emit('install', n)"><AppIcon name="redo" /> {{ $t('نصب و به‌روزرسانی دوباره') }}</button>
      <button class="btn small danger" :disabled="n.state === 'installing'" @click="emit('remove', n)"><AppIcon name="trash" /> {{ $t('حذف سرور') }}</button>
    </div>
  </div>
</template>

<style>
.srv-card { display: flex; flex-direction: column; gap: 14px; }
.srv-card.self { box-shadow: inset 0 0 0 1px rgba(84, 94, 210, 0.35), 0 20px 50px -30px rgba(84, 94, 210, 0.45); }
.srv-head { display: flex; gap: 12px; align-items: center; }
.srv-ic { width: 42px; height: 42px; flex: none; display: grid; place-items: center; border-radius: 12px; font-size: 21px; color: var(--accent-2);
  background: rgba(84, 94, 210, 0.08); border: 1px solid rgba(84, 94, 210, 0.3); }
.srv-ic.iran { color: var(--ok); background: rgba(62, 224, 161, 0.07); border-color: rgba(62, 224, 161, 0.3); }
.srv-ic.kharej { color: #4cc3ff; background: rgba(76, 195, 255, 0.07); border-color: rgba(76, 195, 255, 0.3); }
.srv-title { flex: 1; min-width: 0; }
.srv-title h3 { font-size: 16px; color: #fff; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.srv-sub { display: flex; gap: 6px; font-size: 12.5px; color: var(--ti-2); flex-wrap: wrap; }
.srv-pills { display: flex; gap: 6px; flex-wrap: wrap; justify-content: flex-end; }
.pill.info { color: #7fd3ff; border-color: rgba(76, 195, 255, 0.4); background: rgba(76, 195, 255, 0.07); }
.srv-installing { all: unset; cursor: pointer; display: grid; gap: 6px; padding: 10px 12px; border-radius: 10px; background: rgba(255, 194, 77, 0.06); border: 1px solid rgba(255, 194, 77, 0.25); color: var(--warn); font-size: 13px; }
.srv-installing small { color: var(--ti-3); }
.srv-installing .srv-progress { margin: 0; }
.srv-err { margin: 0; display: grid; gap: 8px; }
.srv-res { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px 12px; align-items: center; }
.srv-ring { display: flex; align-items: center; gap: 8px; min-width: 0; }
.srv-ring svg { width: 40px; height: 40px; flex: none; transform: rotate(-90deg); }
.srv-ring circle { fill: none; stroke-width: 3.5; }
.srv-ring .track { stroke: rgba(255, 255, 255, 0.07); }
.srv-ring .meter { stroke: var(--accent); stroke-linecap: round; transition: stroke-dashoffset 0.6s ease; filter: drop-shadow(0 0 4px rgba(84, 94, 210, 0.6)); }
.srv-ring.hot .meter { stroke: var(--bad); }
.srv-ring div { display: flex; flex-direction: column; min-width: 0; }
.srv-ring b { font-size: 14px; color: #fff; }
.srv-ring small, .srv-net small { font-size: 10.5px; color: var(--ti-3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.srv-net { grid-column: 1 / -1; display: flex; flex-wrap: wrap; align-items: center; gap: 4px 16px; font-size: 12.5px; color: var(--ti-2); min-width: 0;
  padding-top: 8px; border-top: 1px dashed var(--line); }
.srv-net small { margin-inline-start: auto; }
.srv-net i { display: inline-block; width: 8px; height: 3px; border-radius: 2px; margin-inline-end: 4px; vertical-align: middle; }
.srv-net i.rx { background: var(--accent); } .srv-net i.tx { background: #c7cdd3; }
.srv-net b { color: #fff; font-weight: 600; }
.srv-wait { font-size: 13px; }
.srv-sec { display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 13.5px; color: var(--ti-2); margin-bottom: 8px; }
.srv-sec .ic { color: var(--accent-2); }
.srv-sp { flex: 1; }
.srv-bench-vals { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
.srv-bench-vals div { display: flex; flex-direction: column; gap: 2px; padding: 8px 10px; border-radius: 9px; background: rgba(84, 94, 210, 0.05); border: 1px solid rgba(84, 94, 210, 0.16); min-width: 0; }
.srv-bench-vals small { font-size: 11px; color: var(--ti-3); }
.srv-bench-vals b { font-size: 13px; color: #fff; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.srv-tun-row { display: flex; align-items: center; gap: 8px; padding: 7px 0; border-top: 1px dashed var(--line); font-size: 13px; flex-wrap: wrap; }
.srv-tun-row .peer { color: #fff; font-weight: 600; }
.srv-tun-row .kind { color: var(--ti-2); font-size: 12px; }
.srv-tun-row .st { margin-inline-start: auto; font-size: 12px; color: var(--ti-3); }
.srv-tun-row .st.st-up { color: var(--ok); } .srv-tun-row .st.st-down, .srv-tun-row .st.st-missing { color: var(--bad); } .srv-tun-row .st.st-warn { color: var(--warn); }
.srv-actions { margin-top: auto; padding-top: 10px; border-top: 1px solid var(--line); }
@media (max-width: 560px) { .srv-bench-vals { grid-template-columns: 1fr 1fr; } }
</style>

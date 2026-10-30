<script setup>
import { ref, shallowRef, computed } from 'vue';
import { api } from '../lib/api.js';
import { usePoll } from '../lib/poll.js';
import { fail, toast } from '../lib/toast.js';
import { confirmBox } from '../lib/confirm.js';
import { t } from '../lib/i18n.js';
import { fa } from '../lib/format.js';
import { cableState } from '../lib/fleet.js';
import PageHead from '../components/PageHead.vue';
import ServerCard from '../components/ServerCard.vue';
import AddServerWindow from '../components/AddServerWindow.vue';
import GlassWindow from '../components/GlassWindow.vue';
import AppIcon from '../components/AppIcon.vue';
import BenchCard from '../topology/BenchCard.vue';
import '../styles/topology.css';

// The fleet: the mother server first, then every Iran and Kharej server it
// manages, each as a card with specs, live resources, benchmark and tunnels.
const fleet = shallowRef(null);
const bench = shallowRef({ running: {}, results: {} });
const adding = ref(null); // { node?, watch? } while the add window is open
const details = ref(null);

const { refresh } = usePoll(async () => {
  const [f, b] = await Promise.all([api('GET', '/api/fleet'), api('GET', '/api/topology/bench').catch(() => null)]);
  fleet.value = f;
  if (b) bench.value = { running: b.running || {}, results: b.results || {} };
}, 4000);

const nodes = computed(() => {
  const list = (fleet.value && fleet.value.nodes) || [];
  return [...list].sort((a, b) => (a.kind === 'self' ? -1 : b.kind === 'self' ? 1 : (a.added_at || 0) - (b.added_at || 0)));
});
const byId = computed(() => Object.fromEntries(nodes.value.map((n) => [n.id, n])));
const links = computed(() => (fleet.value && fleet.value.links) || []);
const counts = computed(() => {
  const ns = nodes.value.filter((n) => n.kind !== 'self' && n.state);
  return {
    iran: ns.filter((n) => n.kind === 'iran').length,
    kharej: ns.filter((n) => n.kind === 'kharej').length,
    ready: ns.filter((n) => n.state === 'ready' && !(n.stats && n.stats.error)).length,
    total: ns.length,
    up: links.value.filter((l) => cableState(l, byId.value) === 'up').length,
    tunnels: links.value.filter((l) => l.tunnel).length,
  };
});
// devices dropped in the designer but never added
const drafts = computed(() => nodes.value.filter((n) => n.kind !== 'self' && !n.state));
const servers = computed(() => nodes.value.filter((n) => n.kind === 'self' || n.state));

async function runBench(n) {
  try {
    await api('POST', '/api/topology/bench', { id: n.id });
    toast(t('بنچمارک شروع شد؛ چند دقیقه طول می‌کشد'), 'ok');
    refresh();
  } catch (e) { fail(e); }
}

async function remove(n) {
  const own = links.value.filter((l) => (l.a === n.id || l.b === n.id) && l.tunnel).length;
  const ok = await confirmBox(t('حذف سرور «{name}»', { name: n.label || n.ip }),
    (own ? t('تانل‌های این سرور ({n}) از روی همهٔ سرورهای درگیر برداشته می‌شوند.', { n: fa(own) }) + ' ' : '') + t('کلید سرور مادر هم از سرور پاک می‌شود. خود سرور و سرویس‌های دیگرش دست‌نخورده می‌مانند.'), t('حذف'), true);
  if (!ok) return;
  try {
    const r = await api('POST', '/api/fleet/remove', { id: n.id, purge: true });
    toast(t((r.warnings || []).length ? 'سرور حذف شد (با هشدار)' : 'سرور حذف شد'), (r.warnings || []).length ? 'err' : 'ok');
    refresh();
  } catch (e) { fail(e); }
}
</script>

<template>
  <div>
    <PageHead :title="$t('سرورها')" tag="FLEET // MOTHER SERVER">
      <router-link to="/topology" class="btn"><AppIcon name="node" /> {{ $t('طراحی شبکه') }}</router-link>
      <button class="btn primary" @click="adding = {}"><AppIcon name="plus" /> {{ $t('افزودن سرور') }}</button>
    </PageHead>

    <div class="grid fleet-stats">
      <div class="card"><div class="tag">SERVERS</div><div class="stat-value num">{{ fa(counts.total) }}</div><div class="stat-sub">{{ $t('{n} آنلاین', { n: fa(counts.ready) }) }}</div></div>
      <div class="card"><div class="tag">IRAN</div><div class="stat-value num">{{ fa(counts.iran) }}</div><div class="stat-sub">{{ $t('سرور ایران') }}</div></div>
      <div class="card"><div class="tag">KHAREJ</div><div class="stat-value num">{{ fa(counts.kharej) }}</div><div class="stat-sub">{{ $t('سرور خارج') }}</div></div>
      <div class="card"><div class="tag">TUNNELS</div><div class="stat-value num">{{ fa(counts.up) }}<small class="faint"> / {{ fa(counts.tunnels) }}</small></div><div class="stat-sub">{{ $t('تانل متصل') }}</div></div>
    </div>

    <div v-if="!fleet" class="boot"><div class="boot-mark"></div></div>
    <template v-else>
      <div class="fleet-grid">
        <ServerCard v-for="n in servers" :key="n.id" :n="n" :by-id="byId" :links="links" :bench="bench"
          @bench="runBench" @details="details = $event" @install="adding = { node: $event }"
          @progress="adding = { node: $event, watch: $event.id }" @remove="remove" />
        <button class="card fleet-add" @click="adding = {}">
          <AppIcon name="plus" /><b>{{ $t('افزودن سرور ایران یا خارج') }}</b>
          <small>{{ $t('IP و رمز SSH بدهید؛ به‌روزرسانی، نصب پیش‌نیازها و بنچمارک خودکار انجام می‌شود.') }}</small>
        </button>
      </div>
      <div v-if="drafts.length" class="hint">
        {{ $t('{n} سرور در طراحی شبکه هست که هنوز اضافه نشده:', { n: fa(drafts.length) }) }}
        <a v-for="d in drafts" :key="d.id" href="#" class="fleet-draft" @click.prevent="adding = { node: d }">{{ d.label || d.ip || d.id }}</a>
      </div>
    </template>

    <AddServerWindow v-if="adding" :node="adding.node" :watch="adding.watch || ''" @close="adding = null; refresh()" @added="refresh()" />
    <GlassWindow v-if="details" :title="$t('بنچمارک {name}', { name: details.label || details.host })" icon="gauge" :width="620" @close="details = null">
      <BenchCard :node="details" :bench="bench" @run="runBench(details)" />
    </GlassWindow>
  </div>
</template>

<style>
.fleet-stats { margin-bottom: 16px; }
.fleet-stats .stat-value small { font-size: 15px; }
.fleet-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 430px), 1fr)); gap: 16px; align-items: start; }
.fleet-grid > .card + .card { margin-top: 0; }
.fleet-add { all: unset; box-sizing: border-box; cursor: pointer; display: grid; place-items: center; gap: 8px; text-align: center; min-height: 220px; padding: 24px;
  border-radius: var(--radius); border: 1.5px dashed rgba(84, 94, 210, 0.35); color: var(--ti-2); transition: border-color 0.2s, background 0.2s; }
.fleet-add:hover { border-color: var(--accent-2); background: rgba(84, 94, 210, 0.05); color: var(--ti); }
.fleet-add .ic { font-size: 30px; color: var(--accent-2); }
.fleet-add small { color: var(--ti-3); max-width: 280px; line-height: 1.8; }
.fleet-draft { margin-inline-start: 8px; }
@media (max-width: 900px) { .fleet-stats { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>

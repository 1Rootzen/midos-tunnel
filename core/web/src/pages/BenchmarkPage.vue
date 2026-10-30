<script setup>
import { ref, computed, onMounted } from 'vue';
import { api } from '../lib/api.js';
import { fail, toast } from '../lib/toast.js';
import { fa, ms } from '../lib/format.js';
import { t, pct, fwd } from '../lib/i18n.js';
import { nodeName, cableKindText, KERNEL_MODES } from '../lib/fleet.js';
import { TRANSPORTS, trName } from '../lib/constants.js';
import PageHead from '../components/PageHead.vue';
import FormField from '../components/FormField.vue';
import AppIcon from '../components/AppIcon.vue';

// Path test: pick (or type) a server and a few TCP ports and see what gets
// through and how fast, before committing to a transport. It runs from this
// server or from one of the fleet's; a tunnel the designer built between two
// other servers is tested from the end that dials, toward the other.
const TCP_TRANSPORTS = ['tls', 'ws', 'tcp', 'stealth', 'grpc'];
const host = ref('');
const ports = ref([{ port: '', transport: 'tls' }]);
const pick = ref('');
const tunnels = ref([]);
const from = ref('self');
const sources = ref([]); // fleet servers the test can run from
const cables = ref([]); // tunnels between two other servers
const running = ref(false);
const result = ref(null);

function fromCable(c) {
  from.value = c.from;
  host.value = c.host;
  if (c.kind === 'kernel') {
    // a kernel tunnel rides MOI/ESP, which has no handshake to time: test the
    // route and the ports a relay between the two would use
    if (!ports.value.some((p) => p.port)) ports.value = [{ port: '443', transport: 'tls' }];
    return;
  }
  ports.value = [{ port: String(c.port || 443), transport: TCP_TRANSPORTS.includes(c.transport) ? c.transport : 'tcp' }];
}

async function fromTunnel() {
  if (!pick.value) return;
  const c = cables.value.find((x) => x.id === pick.value);
  if (c) { fromCable(c); return; }
  from.value = 'self';
  try {
    const t = await api('GET', '/api/tunnels/' + encodeURIComponent(pick.value));
    const c = t.config || {};
    const eps = (c.endpoints && c.endpoints.length) ? c.endpoints : (c.server ? [{ server: c.server, transport: c.transport || 'tls' }] : []);
    if (!eps.length) { toast(t('این تانل سمت سرور است؛ آدرس مقصد را دستی وارد کنید'), 'err'); return; }
    host.value = eps[0].server.split(':')[0];
    const rows = eps.filter((e) => e.server.includes(':')).map((e) => ({ port: e.server.split(':').pop(), transport: TCP_TRANSPORTS.includes(e.transport) ? e.transport : 'tcp' }));
    ports.value = rows.length ? rows : [{ port: '', transport: 'tls' }];
  } catch (e) { fail(e); }
}

async function run() {
  const rows = ports.value.map((p) => ({ port: parseInt(p.port, 10) || 0, transport: p.transport })).filter((p) => p.port > 0);
  if (!host.value.trim()) { toast(t('آدرس سرور را وارد کنید'), 'err'); return; }
  if (!rows.length) { toast(t('حداقل یک پورت اضافه کنید'), 'err'); return; }
  running.value = true;
  result.value = null;
  try { result.value = await api('POST', '/api/benchmark', { host: host.value.trim(), from: from.value, ports: rows }); }
  catch (e) { fail(e); }
  running.value = false;
}

const probes = computed(() => (result.value ? result.value.probes.slice().sort((a, b) => {
  if (a.reachable !== b.reachable) return a.reachable ? -1 : 1;
  return (a.avg_ms || 1e9) - (b.avg_ms || 1e9);
}) : []));
const maxRtt = computed(() => Math.max(1, ...probes.value.filter((p) => p.reachable).map((p) => p.avg_ms || 0)));

onMounted(async () => {
  try {
    const ts = await api('GET', '/api/tunnels');
    tunnels.value = ts.filter((t) => { const c = t.config || {}; return (c.endpoints && c.endpoints.length) || c.server; }).map((t) => t.id);
  } catch (_) { /* the picker is optional */ }
  try {
    const f = await api('GET', '/api/fleet');
    const byId = Object.fromEntries(f.nodes.map((n) => [n.id, n]));
    sources.value = f.nodes.filter((n) => n.kind !== 'self' && n.state === 'ready').map((n) => ({ id: n.id, name: nodeName(n) }));
    cables.value = (f.links || []).filter((l) => l.tunnel && byId[l.a] && byId[l.b] && byId[l.a].kind !== 'self' && byId[l.b].kind !== 'self')
      .map((l) => {
        const [iran, kharej] = byId[l.a].role === 'kharej' ? [byId[l.b], byId[l.a]] : [byId[l.a], byId[l.b]];
        // a reverse tunnel is dialled by the Kharej end
        const [src, dst] = l.kind === 'direct' && l.reverse ? [kharej, iran] : [iran, kharej];
        return { id: 'net:' + l.id, from: src.id, host: dst.host || dst.ip, kind: l.kind, port: l.port, transport: l.transport,
          label: nodeName(iran) + ' ' + fwd() + ' ' + nodeName(kharej) + ' · ' + cableKindText(l, KERNEL_MODES) };
      });
  } catch (_) { /* no fleet: the test runs from this server */ }
});
</script>

<template>
  <div>
    <PageHead :title="$t('تست مسیر')" tag="PATH TEST // REACHABILITY" />
    <div class="card">
      <FormField :label="$t('سرور مقصد')">
        <div class="row-wrap">
          <input v-model="host" type="text" dir="ltr" :placeholder="$t('مثلاً 203.0.113.9 یا example.com')" />
          <select v-model="pick" @change="fromTunnel"><option value="">{{ $t('انتخاب از تانل‌ها…') }}</option><option v-for="id in tunnels" :key="id" :value="id">{{ id }}</option>
            <option v-for="c in cables" :key="c.id" :value="c.id">{{ c.label }}</option></select>
        </div>
      </FormField>
      <FormField v-if="sources.length" :label="$t('تست از')">
        <select v-model="from"><option value="self">{{ $t('این سرور') }}</option><option v-for="n in sources" :key="n.id" :value="n.id">{{ n.name }}</option></select>
      </FormField>
      <FormField :label="$t('پورت‌ها (ترنسپورت‌های مبتنی بر TCP)')">
        <div v-for="(p, i) in ports" :key="i" class="port-row">
          <input v-model="p.port" type="number" min="1" max="65535" dir="ltr" />
          <select v-model="p.transport"><option v-for="k in TCP_TRANSPORTS" :key="k" :value="k">{{ TRANSPORTS[k].name }}</option></select>
          <button class="btn small icon-only" :title="$t('حذف')" @click="ports.splice(i, 1)"><AppIcon name="close" /></button>
        </div>
        <button class="btn small" @click="ports.push({ port: '', transport: 'tls' })"><AppIcon name="plus" /> {{ $t('افزودن پورت') }}</button>
      </FormField>
      <button class="btn primary" :disabled="running" @click="run"><span v-if="running" class="spin"></span><AppIcon v-else name="gauge" /> {{ $t('اجرای تست') }}</button>
      <p class="hint">{{ $t('KCP/QUIC/DNS روی UDP و ICMP از یک‌سو قابل زمان‌سنجی نیستند؛ این تست دسترس‌پذیری و تأخیر دست‌دادن TCP را می‌سنجد.') }}</p>
    </div>

    <div v-if="running" class="card bench-wait"><span class="spin"></span> {{ $t('در حال تست… (چند ثانیه)') }}</div>
    <template v-if="result">
      <div class="card reco" :class="{ bad: !result.recommend.ok }">
        <div class="tag">{{ $t('پیشنهاد') }}<template v-if="result.from"> · {{ $t('از «{name}»', { name: result.from }) }}</template></div>
        <div v-if="result.recommend.ok" class="reco-line">
          <AppIcon name="check" draw /> <b>{{ $t('پورت') }} <span class="num">{{ fa(result.recommend.port) }}</span></b>
          · {{ trName(result.recommend.transport) }} · <span class="faint num">{{ ms(result.recommend.avg_ms) }}</span>
        </div>
        <div v-else>{{ $t('هیچ پورتی پاسخ نداد؛ احتمالاً مسدود است یا سرویس بالا نیست.') }}</div>
      </div>
      <div class="card">
        <div class="card-head"><h3><AppIcon name="pulse" /> {{ $t('مسیر ICMP (ping)') }}</h3></div>
        <div v-if="result.icmp.error" class="muted">{{ $t('پاسخی از ping نیامد ({err}) — بسیاری از سرورها به ping جواب نمی‌دهند.', { err: result.icmp.error }) }}</div>
        <div v-else class="kv-row">
          <div class="tcell"><div class="tag">LOSS</div><div class="v num">{{ fa(result.icmp.loss_pct) }}{{ pct() }}</div></div>
          <div class="tcell"><div class="tag">RTT</div><div class="v num">{{ ms(result.icmp.avg_ms) }}</div></div>
        </div>
      </div>
      <div class="card">
        <div class="card-head"><h3><AppIcon name="gauge" /> {{ $t('پورت‌ها') }}</h3></div>
        <div class="tunnels">
          <div v-for="p in probes" :key="p.port + p.transport" class="lrow card" :class="{ off: !p.reachable }">
            <div class="tname"><i class="dot" :class="p.reachable ? 'ok' : 'bad'"></i><span class="ltr">:{{ p.port }}</span><span class="pill">{{ trName(p.transport) }}</span></div>
            <div class="tcell"><div class="tag">STATUS</div><div class="v">{{ p.reachable ? $t('باز') : (p.error || $t('بسته')) }}</div></div>
            <div class="tcell grow"><div class="tag">RTT</div>
              <div v-if="p.reachable" class="rtt-cell"><span class="num">{{ ms(p.avg_ms) }}</span><div class="ubar"><div class="ufill" :style="{ width: Math.max(2, 100 * (p.avg_ms || 0) / maxRtt) + '%' }"></div></div></div>
              <span v-else class="faint">—</span>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style>
.row-wrap { display: flex; gap: 8px; flex-wrap: wrap; }
.row-wrap > input { flex: 1 1 220px; }
.row-wrap > select { flex: 0 1 220px; }
.port-row { display: flex; gap: 8px; align-items: center; margin-bottom: 8px; }
.port-row input { width: 130px; }
.port-row select { width: 150px; }
.bench-wait, .reco { margin-top: 16px; }
.reco-line { font-size: 16px; margin-top: 4px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.reco-line .ic { color: var(--ok); font-size: 20px; }
.reco.bad { box-shadow: inset -3px 0 0 var(--bad); }
.kv-row { display: flex; gap: 28px; flex-wrap: wrap; }
.rtt-cell { display: flex; align-items: center; gap: 10px; }
.rtt-cell .ubar { flex: 1 1 auto; }
</style>

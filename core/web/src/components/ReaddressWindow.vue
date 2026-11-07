<script setup>
import { ref, computed, onMounted } from 'vue';
import { api } from '../lib/api.js';
import { fail, toast } from '../lib/toast.js';
import { t, fwd } from '../lib/i18n.js';
import { nodeName } from '../lib/fleet.js';
import GlassWindow from './GlassWindow.vue';
import FormField from './FormField.vue';
import AppIcon from './AppIcon.vue';

// After a provider gives a server a new IP: store it and rebuild every tunnel
// the server is part of. The mother's own address is found on its own, so for
// it this only rebuilds its tunnels.
const props = defineProps({ node: { type: Object, required: true } });
const emit = defineEmits(['close', 'done']);

const self = props.node.kind === 'self';
const ip = ref('');
const port = ref(props.node.ssh_port || 22);
const busy = ref(false);
const out = ref('');
const finished = ref(false);
const tunnels = ref([]);

const steps = (results) => (results || []).map((s) => (s.ok ? '✅ ' : '❌ ') + s.step + '\n' + (s.error ? s.error + '\n' : '') + (s.output || '').trim()).join('\n\n');

onMounted(async () => {
  try {
    const f = await api('GET', '/api/fleet');
    const byId = Object.fromEntries(f.nodes.map((n) => [n.id, n]));
    tunnels.value = (f.links || []).filter((l) => l.tunnel && (l.a === props.node.id || l.b === props.node.id))
      .map((l) => nodeName(byId[l.a] || {}) + ' ' + fwd() + ' ' + nodeName(byId[l.b] || {}) + ' · ' + (l.kind === 'kernel' ? t('کرنلی') : l.tunnel));
  } catch (_) { /* the list is only informative */ }
});

const changed = computed(() => self || (ip.value.trim() && ip.value.trim() !== props.node.ip));

async function submit() {
  busy.value = true;
  out.value = self ? t('در حال ساخت دوبارهٔ تانل‌ها…') : t('در حال بررسی سرور روی IP تازه و ساخت دوبارهٔ تانل‌ها…');
  try {
    const r = await api('POST', '/api/fleet/readdress', { id: props.node.id, ip: ip.value.trim(), ssh_port: Number(port.value) || 0 });
    out.value = steps(r.results) || t('این سرور تانل اعمال‌شده‌ای ندارد.');
    finished.value = true;
    toast(self ? t('تانل‌ها دوباره ساخته شدند') : t('IP تغییر کرد و تانل‌ها دوباره ساخته شدند'), 'ok');
    emit('done');
  } catch (e) {
    out.value = (e.data && e.data.results) ? steps(e.data.results) : '';
    fail(e);
    emit('done');
  }
  busy.value = false;
}
</script>

<template>
  <GlassWindow :title="self ? $t('ساخت دوبارهٔ تانل‌های {name}', { name: nodeName(node) }) : $t('تغییر IP سرور {name}', { name: nodeName(node) })" icon="redo" :width="620" :closable="!busy" @close="emit('close')">
    <div class="readdr">
      <template v-if="!self">
        <p class="muted">{{ $t('وقتی شرکت ارائه‌دهنده IP سرور را عوض کرد، IP تازه را اینجا بدهید. پنل سرور را روی IP تازه بررسی می‌کند (باید همان سرور باشد) و همهٔ تانل‌هایش را روی هر دو سر با IP تازه دوباره می‌سازد.') }}</p>
        <div class="row">
          <FormField :label="$t('IP فعلی')"><input :value="node.ip" type="text" dir="ltr" disabled /></FormField>
          <FormField :label="$t('IP تازه')"><input v-model.trim="ip" type="text" dir="ltr" placeholder="203.0.113.10" :disabled="busy || finished" /></FormField>
          <FormField :label="$t('پورت SSH')"><input v-model.number="port" type="number" dir="ltr" min="1" max="65535" :disabled="busy || finished" /></FormField>
        </div>
      </template>
      <p v-else class="muted">{{ $t('آدرس سرور مادر خودکار پیدا می‌شود ({ip}). اگر IP آن عوض شده، تانل‌هایش را اینجا دوباره بسازید.', { ip: node.host || '—' }) }}</p>
      <div class="section-title">{{ $t('تانل‌هایی که دوباره ساخته می‌شوند') }}</div>
      <ul v-if="tunnels.length" class="readdr-list"><li v-for="(x, i) in tunnels" :key="i" dir="auto">{{ x }}</li></ul>
      <p v-else class="faint">{{ $t('این سرور تانل اعمال‌شده‌ای ندارد.') }}</p>
      <p class="hint warn"><AppIcon name="alert" /> {{ $t('کاربران این تانل‌ها چند ثانیه قطع می‌شوند؛ تانل کرنلی یک سرور ایران با همهٔ سرورهای خارجش دوباره ساخته می‌شود.') }}</p>
      <pre v-if="out" class="logs">{{ out }}</pre>
      <div class="form-actions">
        <button v-if="!finished" type="button" class="btn primary" :disabled="busy || !changed" @click="submit">
          <span v-if="busy" class="spin"></span><AppIcon v-else name="redo" /> {{ self ? $t('ساخت دوبارهٔ تانل‌ها') : $t('تغییر IP و ساخت دوبارهٔ تانل‌ها') }}</button>
        <button type="button" class="btn" :disabled="busy" @click="emit('close')">{{ $t('بستن') }}</button>
      </div>
    </div>
  </GlassWindow>
</template>

<style>
.readdr-list { margin: 6px 0 10px; padding-inline-start: 18px; font-size: 13px; }
.readdr .hint.warn { display: flex; align-items: center; gap: 8px; }
.readdr .logs { margin-top: 10px; max-height: 260px; }
</style>

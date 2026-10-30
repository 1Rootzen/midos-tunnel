<script setup>
import { ref, reactive, computed, nextTick, onMounted, onBeforeUnmount } from 'vue';
import { api } from '../lib/api.js';
import { fail, toast } from '../lib/toast.js';
import { t } from '../lib/i18n.js';
import { STEP_ICON } from '../lib/fleet.js';
import GlassWindow from './GlassWindow.vue';
import FormField from './FormField.vue';
import SegControl from './SegControl.vue';
import AppIcon from './AppIcon.vue';

// Adds a server to the fleet - or sets one up again - and follows its install
// step by step: the mother logs in once with the password, installs its own
// key, updates the system, installs everything a tunnel needs and benchmarks it.
const props = defineProps({
  node: { type: Object, default: null }, // prefill: an existing device or server
  watch: { type: String, default: '' }, // open straight on an install in progress
});
const emit = defineEmits(['close', 'added']);

const n = props.node || {};
const f = reactive({
  label: n.label || '', kind: n.kind === 'iran' ? 'iran' : 'kharej', ip: n.ip || '',
  ssh_user: n.ssh_user || 'root', ssh_port: n.ssh_port || 22, password: '', key: '',
  upgrade: true, bench: true,
});
const managed = !!(n.state && n.host_key);
const id = ref(props.watch || '');
const view = ref(null);
const busy = ref(false);
const logBox = ref(null);
const TOTAL = 8;

const job = computed(() => view.value && view.value.job);
const progress = computed(() => {
  if (!job.value) return 0;
  const done = job.value.steps.filter((s) => s.state !== 'run').length;
  return Math.round((100 * (job.value.done ? TOTAL : done)) / TOTAL);
});
const title = computed(() => (id.value ? t('نصب سرور {name}', { name: f.label || f.ip }) : t(managed ? 'نصب دوبارهٔ سرور' : 'افزودن سرور')));

let timer = 0;
async function poll() {
  clearTimeout(timer);
  try {
    const d = await api('GET', '/api/fleet');
    view.value = d.nodes.find((x) => x.id === id.value) || null;
    await nextTick();
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight;
  } catch (_) { /* the next poll retries */ }
  if (!job.value || !job.value.done) timer = setTimeout(poll, 1500);
}

async function submit() {
  busy.value = true;
  try {
    const r = await api('POST', '/api/fleet/add', {
      id: n.id && n.kind !== 'self' ? n.id : '', label: f.label.trim(), kind: f.kind, ip: f.ip.trim(),
      ssh_user: f.ssh_user.trim(), ssh_port: Number(f.ssh_port) || 22, password: f.password, key: f.key,
      upgrade: f.upgrade, bench: f.bench,
    });
    f.password = ''; f.key = '';
    id.value = r.id;
    emit('added', r.id);
    toast(t('نصب شروع شد'), 'ok');
    poll();
  } catch (e) { fail(e); }
  busy.value = false;
}

onMounted(() => { if (id.value) poll(); });
onBeforeUnmount(() => clearTimeout(timer));
</script>

<template>
  <GlassWindow :title="title" icon="cpu" :width="620" @close="emit('close')">
    <form v-if="!id" class="add-srv" @submit.prevent="submit">
      <p class="muted">{{ $t('سرور مادر با این دسترسی یک بار وارد سرور می‌شود و کلید خودش را نصب می‌کند. بعد از آن سیستم به‌روز می‌شود و پیش‌نیازها، midos-tunnel و پایهٔ تانل کرنلی خودکار نصب می‌شوند. رمز SSH ذخیره نمی‌شود.') }}</p>
      <div class="grid-2">
        <FormField :label="$t('نام سرور')"><input v-model="f.label" type="text" maxlength="40" :placeholder="$t('مثلاً de-hetzner')" /></FormField>
        <FormField :label="$t('نوع سرور')"><SegControl v-model="f.kind" :options="[['iran', $t('ایران')], ['kharej', $t('خارج')]]" /></FormField>
      </div>
      <FormField :label="$t('IP سرور')"><input v-model="f.ip" type="text" dir="ltr" placeholder="203.0.113.10" required :disabled="managed" /></FormField>
      <div class="grid-2">
        <FormField :label="$t('کاربر SSH')"><input v-model="f.ssh_user" type="text" dir="ltr" :disabled="managed" /></FormField>
        <FormField :label="$t('پورت SSH')"><input v-model.number="f.ssh_port" type="number" dir="ltr" min="1" max="65535" :disabled="managed" /></FormField>
      </div>
      <FormField :label="$t('رمز SSH')" :hint="$t(managed ? 'این سرور قبلاً اضافه شده؛ بدون رمز با کلید سرور مادر نصب می‌شود.' : 'فقط برای همین نصب استفاده و بعد دور ریخته می‌شود.')">
        <input v-model="f.password" type="password" dir="ltr" autocomplete="off" :required="!managed && !f.key.trim()" />
      </FormField>
      <details class="adv"><summary>{{ $t('ورود با کلید خصوصی') }}</summary>
        <FormField :label="$t('کلید خصوصی SSH')"><textarea v-model="f.key" dir="ltr" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea></FormField>
      </details>
      <label class="check"><input v-model="f.upgrade" type="checkbox" /> {{ $t('به‌روزرسانی سیستم‌عامل (apt upgrade) — چند دقیقه طول می‌کشد') }}</label>
      <label class="check"><input v-model="f.bench" type="checkbox" /> {{ $t('بنچمارک سرور بعد از نصب (bench.sh)') }}</label>
      <div class="actions">
        <button class="btn primary" type="submit" :disabled="busy"><span v-if="busy" class="spin"></span><AppIcon v-else name="plus" /> {{ $t(managed ? 'نصب دوباره' : 'افزودن و نصب خودکار') }}</button>
        <button class="btn" type="button" @click="emit('close')">{{ $t('انصراف') }}</button>
      </div>
    </form>

    <div v-else class="add-srv">
      <div class="srv-progress"><i :style="{ width: progress + '%' }" :class="{ bad: job && job.done && !job.ok }"></i></div>
      <ol class="srv-steps">
        <li v-for="(s, i) in (job ? job.steps : [])" :key="i" :class="s.state">
          <span class="ic"><span v-if="s.state === 'run'" class="spin"></span><template v-else>{{ STEP_ICON[s.state] }}</template></span>
          <div><b>{{ $t(s.name) }}</b><small v-if="s.note">{{ $t(s.note) }}</small></div>
        </li>
        <li v-if="!job" class="run"><span class="ic"><span class="spin"></span></span><div><b>{{ $t('شروع…') }}</b></div></li>
      </ol>
      <div v-if="job && job.done" class="hint" :class="{ warn: !job.ok }">
        {{ job.ok ? $t('سرور آماده است. حالا می‌توانید در «طراحی شبکه» آن را با سرورهای دیگر تانل کنید.') : $t('نصب کامل نشد: {error}', { error: $t((view && view.error) || '') }) }}
      </div>
      <details class="adv" :open="job && job.done && !job.ok"><summary>{{ $t('خروجی نصب') }}</summary>
        <pre ref="logBox" class="code srv-log">{{ (job && job.tail) || '…' }}</pre>
      </details>
    </div>
    <template v-if="id" #foot>
      <button class="btn" @click="emit('close')">{{ $t(job && job.done ? 'بستن' : 'ادامه در پس‌زمینه') }}</button>
    </template>
  </GlassWindow>
</template>

<style>
.add-srv .grid-2 { gap: 12px; }
.add-srv .field { margin-bottom: 10px; }
.srv-progress { height: 4px; border-radius: 4px; background: rgba(255, 255, 255, 0.06); overflow: hidden; margin-bottom: 14px; }
.srv-progress i { display: block; height: 100%; background: linear-gradient(90deg, #8f96f0, #3d45aa); box-shadow: 0 0 12px rgba(84, 94, 210, 0.7); transition: width 0.5s ease; }
.srv-progress i.bad { background: var(--bad); }
.srv-steps { list-style: none; margin: 0 0 12px; padding: 0; display: grid; gap: 8px; }
.srv-steps li { display: flex; gap: 10px; align-items: flex-start; color: var(--ti-2); }
.srv-steps li .ic { width: 22px; height: 22px; flex: none; display: grid; place-items: center; border-radius: 50%; border: 1px solid var(--line-2); font-size: 12px; font-weight: 700; }
.srv-steps li .ic .spin { width: 12px; height: 12px; }
.srv-steps li div { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.srv-steps li small { color: var(--ti-3); font-size: 12px; }
.srv-steps li.ok .ic { color: var(--ok); border-color: rgba(62, 224, 161, 0.5); }
.srv-steps li.bad .ic { color: var(--bad); border-color: rgba(255, 77, 94, 0.5); }
.srv-steps li.warn .ic { color: var(--warn); border-color: rgba(255, 194, 77, 0.5); }
.srv-steps li.skip { opacity: 0.55; }
.srv-steps li.run b { color: var(--ti); }
.srv-log { max-height: 260px; overflow: auto; white-space: pre-wrap; direction: ltr; text-align: left; font-size: 11.5px; }
</style>

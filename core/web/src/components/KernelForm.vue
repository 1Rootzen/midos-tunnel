<script setup>
import { ref, reactive, computed, watch, onMounted } from 'vue';
import { api } from '../lib/api.js';
import { fail, toast } from '../lib/toast.js';
import { randomKey, fa } from '../lib/format.js';
import { t } from '../lib/i18n.js';
import FormField from './FormField.vue';
import AppIcon from './AppIcon.vue';

// The kernel-tunnel part of the create form: builds an ESP / MOI tunnel on
// this server and, over SSH, on every server at the other end. The role comes
// from the form's "where is this server" choice.
const props = defineProps({
  role: { type: String, required: true }, // IRAN or KHAREJ
  selfHost: { type: String, default: '' },
  ports: { type: String, default: '' },
  udpPorts: { type: String, default: '' },
});
const emit = defineEmits(['done', 'cancel']);

const f = reactive({ mode: 'esp', psk: randomKey().slice(0, 24), selfIP: props.selfHost, mtu: '', watchdog: true, kharejSlot: '' });
const blank = () => ({ ip: '', label: '', slot: '', host: '', port: '', user: '', pass: '', key: '' });
const peers = ref([blank()]);
const out = ref('');
const busy = ref(false);
const existing = ref(null);
const needsPsk = computed(() => f.mode !== 'gre' && f.mode !== 'fou');

watch(() => props.role, () => { peers.value = [blank()]; });

const steps = (results) => (results || []).map((s) => (s.ok ? '✅ ' : '❌ ') + s.step + '\n' + (s.output || s.error || '').trim()).join('\n\n');
const csv = (v) => String(v || '').split(/[\s,،]+/).filter(Boolean).join(',');

async function submit() {
  const body = {
    role: props.role, mode: f.mode, psk: needsPsk.value ? f.psk : '', self_ip: f.selfIP.trim(), mtu: f.mtu.trim(), watchdog: f.watchdog,
    tcp_ports: csv(props.ports), udp_ports: csv(props.udpPorts),
    peers: peers.value.map((p) => ({
      self_ip: p.ip.trim(), label: p.label.trim(), slot: p.slot.trim(),
      ssh: { host: p.host.trim() || p.ip.trim(), port: parseInt(p.port, 10) || 22, user: p.user.trim() || 'root', password: p.pass, key: p.key.trim() },
    })),
    iran_ip: '', slot: '',
  };
  if (props.role === 'KHAREJ') { body.iran_ip = body.peers[0] ? body.peers[0].self_ip : ''; body.slot = f.kharejSlot.trim(); }
  busy.value = true;
  out.value = t('در حال ساخت… (نصب بسته‌ها و برپایی دو سر چند دقیقه طول می‌کشد)');
  try {
    const r = await api('POST', '/api/kernel/create', body);
    out.value = steps(r.results);
    if (r.ok) { toast(t('تانل کرنلی ساخته شد'), 'ok'); setTimeout(() => emit('done'), 1800); }
    else toast(t('بعضی مراحل خطا داشت؛ خروجی را ببینید'), 'err');
  } catch (e) {
    // the per-step results of a failed run ride on the error
    out.value = (e.data && e.data.results) ? steps(e.data.results) : '';
    fail(e);
  }
  busy.value = false;
}

onMounted(async () => {
  try { const k = await api('GET', '/api/kernel'); if (k.installed) existing.value = k; } catch (_) { /* informational only */ }
});
</script>

<template>
  <div class="kform">
    <div v-if="existing" class="card kexist">
      <p class="hint warn"><AppIcon name="alert" /> {{ $t('روی این سرور از قبل یک تانل کرنلی ({mode}، {n} همتا) هست؛ ساخت دوباره آن را بازنویسی می‌کند.', { mode: existing.mode, n: fa(existing.peers.length) }) }}</p>
      <router-link to="/kernel" class="btn small" @click="emit('cancel')"><AppIcon name="shield" /> {{ $t('مدیریت تانل کرنلی فعلی') }}</router-link>
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-head"><h3><AppIcon name="shield" /> {{ $t('تانل کرنلی') }}</h3><span class="tag">ESP / MOI</span></div>
        <p class="muted">{{ $t('پنل این سرور و سرور(های) مقابل را خودکار و هماهنگ برپا می‌کند. دسترسی SSH سرور مقابل لازم است.') }}</p>
        <FormField :label="$t('نوع تانل')">
          <select v-model="f.mode">
            <option value="esp">{{ $t('ESP (رمزدار، توصیه)') }}</option><option value="espudp">ESP-in-UDP</option>
            <option value="gre">MOI</option><option value="fou">FOU</option><option value="ipsec">IPsec</option>
          </select>
        </FormField>
        <div class="row">
          <FormField :label="$t('IP این سرور')"><input v-model="f.selfIP" type="text" dir="ltr" :placeholder="selfHost || $t('IP عمومی این سرور')" /></FormField>
          <FormField :label="$t('MTU (اختیاری)')"><input v-model="f.mtu" type="text" dir="ltr" :placeholder="$t('۱۳۶۰ (پیش‌فرض)')" /></FormField>
        </div>
        <FormField v-if="needsPsk" :label="$t('عبارت‌عبور مشترک')" :hint="$t('همهٔ کلیدها از این و جفت IP مشتق می‌شوند؛ باید در هر دو سر یکی باشد.')">
          <div class="cmd"><input v-model="f.psk" type="text" dir="ltr" />
            <button type="button" class="btn icon-only" :title="$t('عبارت تازه')" @click="f.psk = randomKey().slice(0, 24)"><AppIcon name="key" /></button></div>
        </FormField>
        <label class="check"><input v-model="f.watchdog" type="checkbox" /> {{ $t('دیده‌بان خودکار (بازآوری هنگام قطعی)') }}</label>
        <FormField v-if="role === 'KHAREJ'" :label="$t('اسلات این سرور (خارج)')" :hint="$t('باید با اسلاتی که سرور ایران به این سرور می‌دهد یکی باشد؛ خالی = خودکار.')">
          <input v-model="f.kharejSlot" type="text" dir="ltr" />
        </FormField>
        <p class="hint">{{ $t('پورت‌های منتقل‌شونده همان «پورت‌های TCP/UDP» بالای فرم‌اند؛ خالی یعنی همهٔ پورت‌ها.') }}</p>
      </div>

      <div class="card">
        <div class="card-head">
          <h3><AppIcon name="net" /> {{ $t(role === 'IRAN' ? 'سرورهای مقابل (خارج)' : 'سرور ایران') }}</h3>
          <button v-if="role === 'IRAN'" type="button" class="btn small" @click="peers.push(blank())"><AppIcon name="plus" /> {{ $t('افزودن') }}</button>
        </div>
        <div v-for="(p, i) in peers" :key="i" class="kpeer">
          <div class="kpeer-head"><b>{{ $t('سرور مقابل {n}', { n: fa(i + 1) }) }}</b>
            <button v-if="peers.length > 1" type="button" class="btn small danger icon-only" :title="$t('حذف')" @click="peers.splice(i, 1)"><AppIcon name="trash" /></button></div>
          <div class="row">
            <FormField :label="$t('IP عمومی')"><input v-model="p.ip" type="text" dir="ltr" :placeholder="$t('IP عمومی سرور مقابل')" /></FormField>
            <FormField :label="$t('برچسب')"><input v-model="p.label" type="text" dir="ltr" :placeholder="$t('مثلاً uk')" /></FormField>
          </div>
          <FormField v-if="role === 'IRAN'" :label="$t('اسلات')" :hint="$t('روی هاب هر سرور اسلات یکتا دارد؛ خالی = خودکار')"><input v-model="p.slot" type="text" dir="ltr" /></FormField>
          <div class="row">
            <FormField :label="$t('کاربر SSH')"><input v-model="p.user" type="text" dir="ltr" placeholder="root" /></FormField>
            <FormField :label="$t('رمز SSH')"><input v-model="p.pass" type="password" dir="ltr" autocomplete="new-password" /></FormField>
          </div>
          <details class="adv"><summary>{{ $t('آدرس، پورت یا کلید SSH') }}</summary>
            <div class="row">
              <FormField :label="$t('آدرس SSH')"><input v-model="p.host" type="text" dir="ltr" :placeholder="$t('پیش‌فرض = همان IP')" /></FormField>
              <FormField :label="$t('پورت SSH')"><input v-model="p.port" type="text" dir="ltr" placeholder="22" /></FormField>
            </div>
            <FormField :label="$t('کلید خصوصی (به‌جای رمز، اختیاری)')"><textarea v-model="p.key" dir="ltr" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea></FormField>
          </details>
        </div>
      </div>
    </div>

    <pre v-if="out" class="logs kout">{{ out }}</pre>
    <div class="form-actions">
      <button type="button" class="btn primary" :disabled="busy" @click="submit"><span v-if="busy" class="spin"></span><AppIcon v-else name="shield" /> {{ $t('ساخت تانل کرنلی روی هر دو سر') }}</button>
      <button type="button" class="btn" @click="emit('cancel')">{{ $t('انصراف') }}</button>
    </div>
  </div>
</template>

<style>
.kform .grid-2 { margin-top: 14px; }
.kexist { margin-top: 14px; }
.kexist .hint { display: flex; align-items: center; gap: 8px; margin: 0 0 10px; }
.kpeer { padding: 12px 0; border-top: 1px dashed var(--line-2); }
.kpeer:first-of-type { border-top: 0; padding-top: 0; }
.kpeer-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
.kout { margin-top: 14px; }
</style>

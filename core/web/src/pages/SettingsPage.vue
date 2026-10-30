<script setup>
import { ref, reactive, onMounted } from 'vue';
import { api } from '../lib/api.js';
import { store } from '../lib/store.js';
import { fail, toast } from '../lib/toast.js';
import { confirmBox } from '../lib/confirm.js';
import { t } from '../lib/i18n.js';
import PageHead from '../components/PageHead.vue';
import FormField from '../components/FormField.vue';
import AppIcon from '../components/AppIcon.vue';
import GlassWindow from '../components/GlassWindow.vue';

const s = store.session;

// ---------------------------------------------------------------- password
const pw = reactive({ cur: '', next: '', again: '' });
async function changePassword() {
  if (pw.next !== pw.again) { toast(t('رمز جدید و تکرار آن یکی نیستند'), 'err'); return; }
  try {
    await api('POST', '/api/settings/password', { current: pw.cur, new: pw.next });
    toast(t('رمز تغییر کرد؛ نشست‌های دیگر بسته شدند'), 'ok');
    Object.assign(pw, { cur: '', next: '', again: '' });
  } catch (e) { fail(e); }
}

// ---------------------------------------------------------------- telegram
const tg = reactive({ token: '', chat: '' });
async function saveTelegram() {
  try { await api('POST', '/api/settings/telegram', { token: tg.token.trim(), chat: tg.chat.trim() }); toast(t('ذخیره شد'), 'ok'); s.telegram = !!tg.token.trim(); }
  catch (e) { fail(e); }
}
async function testTelegram() {
  try { await api('POST', '/api/settings/telegram/test'); toast(t('پیام آزمایشی فرستاده شد'), 'ok'); }
  catch (e) { fail(e); }
}

// ---------------------------------------------------------------- alerts
const al = reactive({ webhook: '', watchdog: !!s.watchdog });
async function saveAlerts() {
  try {
    await api('POST', '/api/settings/alerts', { webhook: al.webhook.trim(), watchdog: al.watchdog });
    toast(t('ذخیره شد'), 'ok');
    s.webhook = !!al.webhook.trim(); s.watchdog = al.watchdog;
  } catch (e) { fail(e); }
}

// ---------------------------------------------------------------- update
const up = ref(null);
const upErr = ref('');
async function checkUpdate() {
  try { up.value = await api('GET', '/api/update'); } catch (_) { upErr.value = 'بررسی نسخه ناموفق بود'; }
}
async function applyUpdate() {
  if (!await confirmBox(t('به‌روزرسانی'), t('باینری جدید با بررسی checksum نصب و سرویس‌ها و پنل ری‌استارت می‌شوند.'), t('نصب'))) return;
  try { const r = await api('POST', '/api/update'); toast(t(r.note || 'نصب شد'), 'ok'); } catch (e) { fail(e); }
}
async function rollback() {
  if (!await confirmBox(t('بازگشت'), t('به نسخهٔ قبلی باینری برمی‌گردد و سرویس‌ها ری‌استارت می‌شوند.'), t('بازگشت'), true)) return;
  try { const r = await api('POST', '/api/update/rollback'); toast(t(r.note || 'برگشت'), 'ok'); } catch (e) { fail(e); }
}

// ---------------------------------------------------------------- certificate
const cert = reactive({ cert: '', key: '' });
async function saveCert() {
  try { await api('POST', '/api/settings/cert', { cert: cert.cert, key: cert.key }); toast(t('گواهی ذخیره شد؛ برای اعمال، پنل را ری‌استارت کنید'), 'ok'); }
  catch (e) { fail(e); }
}

// ---------------------------------------------------------------- auto ssl
const acme = reactive({ domain: s.acme_domain || '', method: s.acme_method || 'http-01', email: s.acme_email || '' });
const acmeOpen = ref(false);
const acmeSaved = ref('');
const acmeBusy = ref(false);
const panelPort = location.port || '443';
async function saveACME(restart) {
  if (!acme.domain.trim()) { toast(t('نام دامنه را وارد کنید'), 'err'); return; }
  acmeBusy.value = true;
  try {
    await api('POST', '/api/settings/tls', { domain: acme.domain.trim(), method: acme.method, email: acme.email.trim(), restart });
    Object.assign(s, { acme_domain: acme.domain.trim().toLowerCase(), acme_method: acme.method, acme_email: acme.email.trim() });
    acmeSaved.value = s.acme_domain;
    if (restart) {
      toast(t('پنل در حال ری‌استارت است؛ به آدرس دامنه منتقل می‌شوید'), 'ok');
      setTimeout(() => { location.href = 'https://' + s.acme_domain + (panelPort === '443' ? '' : ':' + panelPort) + location.pathname + location.hash; }, 4000);
    } else toast(t('SSL خودکار ذخیره شد'), 'ok');
  } catch (e) { fail(e); }
  acmeBusy.value = false;
}
async function disableACME() {
  if (!await confirmBox(t('خاموش کردن SSL خودکار'), t('پنل به گواهی خودامضا برمی‌گردد. باید ری‌استارت شود.'), t('خاموش کن'), true)) return;
  try {
    await api('POST', '/api/settings/tls', { domain: '', restart: true });
    toast(t('SSL خودکار خاموش شد؛ پنل در حال ری‌استارت است'), 'ok');
    acme.domain = ''; s.acme_domain = ''; acmeSaved.value = '';
  } catch (e) { fail(e); }
}

// ---------------------------------------------------------------- backup
const file = ref(null);
const restored = ref([]);
async function download() {
  try {
    const blob = await api('GET', '/api/backup');
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'midos-tunnels-' + new Date().toISOString().slice(0, 10) + '.tar.gz';
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 5000);
  } catch (e) { fail(e); }
}
async function restore() {
  const f = file.value && file.value.files[0];
  if (!f) { toast(t('یک فایل پشتیبان انتخاب کنید'), 'err'); return; }
  const fd = new FormData();
  fd.append('file', f);
  try { const r = await api('POST', '/api/restore', fd); restored.value = r.results || []; toast(t('بازگردانی انجام شد'), 'ok'); }
  catch (e) { fail(e); }
}

onMounted(checkUpdate);
</script>

<template>
  <div>
    <PageHead :title="$t('تنظیمات')" tag="CONFIG // SECURITY" />

    <div class="grid-2">
      <form class="card" @submit.prevent="changePassword">
        <div class="card-head"><h3><AppIcon name="key" /> {{ $t('تغییر رمز عبور') }}</h3></div>
        <FormField :label="$t('رمز فعلی')"><input v-model="pw.cur" type="password" autocomplete="current-password" dir="ltr" /></FormField>
        <FormField :label="$t('رمز جدید (حداقل ۱۰ حرف)')"><input v-model="pw.next" type="password" autocomplete="new-password" dir="ltr" minlength="10" /></FormField>
        <FormField :label="$t('تکرار رمز جدید')"><input v-model="pw.again" type="password" autocomplete="new-password" dir="ltr" /></FormField>
        <button class="btn primary" type="submit"><AppIcon name="check" /> {{ $t('ذخیره') }}</button>
      </form>

      <div class="card">
        <div class="card-head"><h3><AppIcon name="gear" /> {{ $t('دربارهٔ پنل') }}</h3><span class="tag">SYSTEM INFO</span></div>
        <div class="kv">
          <div class="k">{{ $t('نسخه') }}</div><div class="ltr">{{ s.version }}</div>
          <div class="k">{{ $t('سرور') }}</div><div class="ltr">{{ s.hostname }}</div>
          <div class="k">{{ $t('آدرس عمومی') }}</div><div class="ltr">{{ s.public_host }}</div>
          <div class="k">{{ $t('کاربر') }}</div><div class="ltr">{{ s.user }}</div>
          <div class="k">{{ $t('فراموشی رمز') }}</div><div class="ltr">midos-tunnel panel passwd</div>
        </div>
      </div>
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-head"><h3><AppIcon name="net" /> {{ $t('هشدار تلگرام') }}</h3><span class="pill" :class="{ ok: s.telegram }">{{ $t(s.telegram ? 'روشن' : 'خاموش') }}</span></div>
        <p class="muted">{{ $t('با ربات تلگرام، هنگام قطع و وصل شدن تانل و تغییر رمز پیام می‌گیرید. توکن را از BotFather و chat id را از ربات @userinfobot بگیرید.') }}</p>
        <FormField :label="$t('توکن ربات')"><input v-model="tg.token" type="text" dir="ltr" placeholder="123456:ABC-DEF..." /></FormField>
        <FormField :label="$t('شناسهٔ چت (chat id)')"><input v-model="tg.chat" type="text" dir="ltr" placeholder="123456789" /></FormField>
        <div class="actions"><button class="btn primary" @click="saveTelegram">{{ $t('ذخیره') }}</button><button class="btn" @click="testTelegram"><AppIcon name="bell" /> {{ $t('پیام آزمایشی') }}</button></div>
        <div class="stat-sub">{{ $t('برای خاموش کردن، هر دو فیلد را خالی بگذارید و ذخیره کنید.') }}</div>
      </div>

      <div class="card">
        <div class="card-head"><h3><AppIcon name="bell" /> {{ $t('هشدار و واچ‌داگ') }}</h3><span class="pill" :class="{ ok: s.webhook || s.watchdog }">{{ $t(s.webhook || s.watchdog ? 'روشن' : 'خاموش') }}</span></div>
        <p class="muted">{{ $t('آدرس وبهوک همان پیام‌های تلگرام را به‌صورت JSON می‌گیرد. واچ‌داگ تانل مدیریت‌شده‌ای را که به خطا خورده، خودکار دوباره راه‌اندازی می‌کند.') }}</p>
        <FormField :label="$t('آدرس وبهوک (اختیاری)')"><input v-model="al.webhook" type="text" dir="ltr" placeholder="https://hooks.example.com/..." /></FormField>
        <label class="check"><input v-model="al.watchdog" type="checkbox" /> {{ $t('راه‌اندازی خودکار تانل خراب (واچ‌داگ)') }}</label>
        <button class="btn primary" @click="saveAlerts">{{ $t('ذخیره') }}</button>
        <div class="stat-sub">{{ $t('برای خاموش کردن وبهوک، فیلد را خالی بگذارید و ذخیره کنید.') }}</div>
      </div>
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-head"><h3><AppIcon name="shield" /> {{ $t('گواهی SSL پنل') }}</h3>
          <span class="pill" :class="{ ok: s.acme_domain }">{{ s.acme_domain ? $t('خودکار: {d}', { d: s.acme_domain }) : $t('خودامضا / دلخواه') }}</span></div>
        <p class="muted">{{ $t('گواهی رایگان و خودکار Let’s Encrypt بگیرید تا مرورگر هشدار ندهد، یا گواهی دلخواه خود را بگذارید. بعد از ذخیره، پنل باید ری‌استارت شود.') }}</p>
        <div class="actions ssl-auto">
          <button class="btn primary" @click="acmeOpen = true; acmeSaved = ''"><AppIcon name="shield" /> {{ $t('دریافت خودکار SSL') }}</button>
          <button v-if="s.acme_domain" class="btn" @click="disableACME"><AppIcon name="close" /> {{ $t('خاموش کردن SSL خودکار') }}</button>
        </div>
        <div class="section-title">{{ $t('گواهی دلخواه (PEM)') }}</div>
        <FormField :label="$t('گواهی (PEM)')"><textarea v-model="cert.cert" dir="ltr" placeholder="-----BEGIN CERTIFICATE-----"></textarea></FormField>
        <FormField :label="$t('کلید خصوصی (PEM)')"><textarea v-model="cert.key" dir="ltr" placeholder="-----BEGIN PRIVATE KEY-----"></textarea></FormField>
        <button class="btn" @click="saveCert"><AppIcon name="up" /> {{ $t('ذخیرهٔ گواهی دلخواه') }}</button>
      </div>

      <div class="card">
        <div class="card-head"><h3><AppIcon name="down" /> {{ $t('به‌روزرسانی') }}</h3></div>
        <div v-if="!up && !upErr" class="muted"><span class="spin"></span> {{ $t('در حال بررسی…') }}</div>
        <div v-else-if="upErr" class="muted">{{ $t(upErr) }}</div>
        <template v-else>
          <div class="kv">
            <div class="k">{{ $t('نسخهٔ فعلی') }}</div><div class="ltr">{{ up.current || '—' }}</div>
            <div class="k">{{ $t('آخرین نسخه') }}</div><div class="ltr">{{ up.latest || (up.error ? $t('نامشخص') : '—') }}</div>
          </div>
          <p class="upd-state">
            <span v-if="up.latest && up.latest !== up.current" class="pill accent">{{ $t('نسخهٔ جدید موجود است') }}</span>
            <span v-else class="faint">{{ up.error || $t('به‌روز هستید') }}</span>
          </p>
        </template>
        <div class="actions">
          <button class="btn primary" :disabled="!up || !up.latest || up.latest === up.current" @click="applyUpdate"><AppIcon name="up" /> {{ $t('نصب به‌روزرسانی') }}</button>
          <button v-if="s.rollback" class="btn" @click="rollback"><AppIcon name="redo" /> {{ $t('بازگشت به نسخهٔ قبل') }}</button>
        </div>
      </div>

      <div class="card">
        <div class="card-head"><h3><AppIcon name="down" /> {{ $t('پشتیبان‌گیری و بازگردانی') }}</h3></div>
        <p class="muted">{{ $t('پشتیبان شامل کانفیگ همهٔ تانل‌ها (با کلیدهایشان) است؛ آن را جای امن نگه دارید.') }}</p>
        <button class="btn primary" @click="download"><AppIcon name="down" /> {{ $t('دانلود پشتیبان') }}</button>
        <div class="section-title">{{ $t('بازگردانی') }}</div>
        <FormField :hint="$t('تانل‌هایی که هم‌نام آن‌ها از قبل وجود دارد دست نمی‌خورند.')">
          <input ref="file" type="file" accept=".tar.gz,.tgz,application/gzip" />
        </FormField>
        <button class="btn" @click="restore"><AppIcon name="up" /> {{ $t('بازگردانی') }}</button>
        <div v-for="x in restored" :key="x.name" class="restored"><span class="ltr">{{ x.name }}</span> — {{ x.status }}</div>
      </div>
    </div>

    <GlassWindow v-if="acmeOpen" :title="$t('دریافت خودکار SSL')" icon="shield" :width="560" :closable="!acmeBusy" @close="acmeOpen = false">
      <div v-if="acmeSaved" class="acme-done">
        <div class="acme-ok"><AppIcon name="check" /></div>
        <p>{{ $t('تنظیم شد. گواهی برای') }} <b class="ltr">{{ acmeSaved }}</b> {{ $t('با اولین بازدید از این آدرس گرفته می‌شود و خودکار تمدید می‌شود:') }}</p>
        <div class="code ltr">https://{{ acmeSaved }}{{ panelPort === '443' ? '' : ':' + panelPort }}</div>
        <div class="actions">
          <button class="btn primary" :disabled="acmeBusy" @click="saveACME(true)"><AppIcon name="redo" :spin="acmeBusy" /> {{ $t('ری‌استارت پنل و رفتن به دامنه') }}</button>
          <button class="btn" @click="acmeOpen = false">{{ $t('بعداً') }}</button>
        </div>
      </div>
      <form v-else @submit.prevent="saveACME(false)">
        <ol class="acme-steps">
          <li>{{ $t('یک دامنه یا ساب‌دامنه بسازید و رکورد') }} <b class="ltr">A</b> {{ $t('آن را به IP این سرور') }} (<span class="ltr">{{ s.public_host }}</span>) {{ $t('بزنید. در کلودفلر پروکسی (ابر نارنجی) را خاموش کنید.') }}</li>
          <li>{{ $t('روش اعتبارسنجی را انتخاب کنید.') }} <b>HTTP-01</b> {{ $t('پورت ۸۰ را لازم دارد؛') }} <b>TLS-ALPN-01</b> {{ $t('فقط وقتی پنل روی پورت ۴۴۳ است کار می‌کند.') }}</li>
          <li>{{ $t('ذخیره کنید و پنل را ری‌استارت کنید. گواهی Let’s Encrypt با اولین بازدید از دامنه گرفته و خودکار تمدید می‌شود.') }}</li>
        </ol>
        <FormField :label="$t('دامنهٔ پنل')"><input v-model="acme.domain" type="text" dir="ltr" placeholder="panel.example.com" autocomplete="off" required /></FormField>
        <FormField :label="$t('روش اعتبارسنجی')">
          <select v-model="acme.method" dir="ltr">
            <option value="http-01">HTTP-01 (port 80)</option>
            <option value="tls-alpn-01" :disabled="panelPort !== '443'">TLS-ALPN-01 (port 443)</option>
          </select>
        </FormField>
        <FormField :label="$t('ایمیل (اختیاری)')" :hint="$t('Let’s Encrypt هشدار انقضا را به این ایمیل می‌فرستد.')"><input v-model="acme.email" type="email" dir="ltr" placeholder="you@example.com" /></FormField>
        <div class="actions">
          <button class="btn primary" type="submit" :disabled="acmeBusy"><span v-if="acmeBusy" class="spin"></span><AppIcon v-else name="shield" /> {{ $t('ذخیره و فعال‌سازی') }}</button>
          <button class="btn" type="button" @click="acmeOpen = false">{{ $t('انصراف') }}</button>
        </div>
      </form>
    </GlassWindow>
  </div>
</template>

<style>
.upd-state { margin: 12px 0; }
.ssl-auto { margin: 4px 0 10px; }
.acme-steps { margin: 0 0 14px; padding-inline-start: 20px; color: var(--ti-2); font-size: 13.5px; line-height: 1.9; }
.acme-done { text-align: center; }
.acme-done .actions { justify-content: center; }
.acme-ok { width: 54px; height: 54px; margin: 4px auto 12px; display: grid; place-items: center; border-radius: 50%; color: var(--ok, #3ddc84); border: 1px solid currentColor; box-shadow: 0 0 22px -4px currentColor; }
.acme-ok svg { width: 26px; height: 26px; }
.acme-done .code { margin: 10px auto 16px; display: inline-block; padding: 8px 14px; border-radius: 8px; background: rgba(84, 94, 210, .08); border: 1px solid rgba(84, 94, 210, .25); color: var(--accent-2); }
.restored { margin-top: 6px; font-size: 13px; color: var(--ti-2); }
</style>

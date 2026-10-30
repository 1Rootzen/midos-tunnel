<script setup>
import { ref, onMounted } from 'vue';
import { api, boot } from '../lib/api.js';
import { fail } from '../lib/toast.js';
import AppIcon from '../components/AppIcon.vue';
import LangSwitch from '../components/LangSwitch.vue';
import logo from '../assets/logo.webp';

const user = ref('');
const pass = ref('');
const cap = ref('');
const capId = ref('');
const capSvg = ref('');
const busy = ref(false);
const userInput = ref(null);

async function loadCaptcha() {
  try {
    const c = await api('GET', '/api/captcha', undefined, { quiet: true });
    capId.value = c.id;
    capSvg.value = c.svg; // drawn by our own server; no user data in it
  } catch (e) { fail(e); }
}

async function submit() {
  busy.value = true;
  try {
    await api('POST', '/api/login', { username: user.value.trim(), password: pass.value, captcha_id: capId.value, captcha: cap.value }, { quiet: true });
    await boot();
  } catch (e) {
    fail(e);
    cap.value = '';
    loadCaptcha();
  }
  busy.value = false;
}

onMounted(() => {
  loadCaptcha();
  userInput.value && userInput.value.focus();
});
</script>

<template>
  <div class="login-wrap">
    <LangSwitch class="login-lang" />
    <form class="login glass" @submit.prevent="submit">
      <span class="tag">SECURE ACCESS // AUTH REQUIRED</span>
      <img class="login-logo" :src="logo" width="168" height="189" alt="Midos Tunnel" decoding="async" fetchpriority="high" />
      <p>{{ $t('برای مدیریت تانل‌ها وارد شوید') }}</p>
      <div class="field">
        <label>{{ $t('نام کاربری') }}</label>
        <div class="in-icon"><input ref="userInput" v-model="user" type="text" autocomplete="username" dir="ltr" required /><AppIcon name="user" /></div>
      </div>
      <div class="field">
        <label>{{ $t('رمز عبور') }}</label>
        <div class="in-icon"><input v-model="pass" type="password" autocomplete="current-password" dir="ltr" required /><AppIcon name="lock" /></div>
      </div>
      <div class="field">
        <label>{{ $t('کد امنیتی') }}</label>
        <div class="captcha-row">
          <div class="captcha-img" :title="$t('تصویر جدید')" @click="loadCaptcha" v-html="capSvg"></div>
          <input v-model="cap" type="text" autocomplete="off" dir="ltr" maxlength="5" required />
        </div>
      </div>
      <button class="btn primary block" type="submit" :disabled="busy">
        <span v-if="busy" class="spin"></span><AppIcon v-else name="shield" /> {{ $t('ورود') }}
      </button>
    </form>
  </div>
</template>

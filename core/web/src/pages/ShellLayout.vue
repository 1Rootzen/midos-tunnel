<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue';
import { useRoute } from 'vue-router';
import { store } from '../lib/store.js';
import { api } from '../lib/api.js';
import AppIcon from '../components/AppIcon.vue';
import PromoBanner from '../components/PromoBanner.vue';
import LangSwitch from '../components/LangSwitch.vue';
import mark from '../assets/logo-mark.webp';

const NAV = [
  ['dashboard', 'داشبورد', 'dash'],
  ['tunnels', 'تانل‌ها', 'tunnel'],
  ['servers', 'سرورها', 'cpu'],
  ['topology', 'طراحی شبکه', 'node'],
  ['benchmark', 'تست مسیر', 'gauge'],
  ['usage', 'ترافیک', 'chart'],
  ['doctor', 'سلامت', 'heart'],
  ['settings', 'تنظیمات', 'gear'],
  ['audit', 'ممیزی', 'list'],
];

const route = useRoute();
const active = computed(() => route.meta.nav || '');
const now = ref('');
let timer;
const tick = () => { now.value = new Date().toLocaleTimeString('en-GB'); };

async function logout() {
  try { await api('POST', '/api/logout'); } catch (_) { /* the session may already be gone */ }
  store.session = null;
}

onMounted(() => { tick(); timer = setInterval(tick, 1000); });
onBeforeUnmount(() => clearInterval(timer));
</script>

<template>
  <div class="shell-top">
    <PromoBanner />
    <header class="top">
      <div class="top-inner">
        <div class="brand">
          <img :src="mark" width="36" height="40" alt="Midos Tunnel" decoding="async" />
          <div class="brand-txt"><b>Midos Tunnel</b><small>CONTROL HUD</small></div>
        </div>
        <nav class="nav">
          <router-link v-for="[key, label, ic] in NAV" :key="key" :to="'/' + key" :class="{ on: active === key }">
            <AppIcon :name="ic" /><span>{{ $t(label) }}</span>
          </router-link>
        </nav>
        <div class="clock">{{ store.session.hostname }} · {{ now }}</div>
        <LangSwitch />
        <button class="btn small icon-only" :title="$t('خروج')" @click="logout"><AppIcon name="out" /></button>
      </div>
    </header>
  </div>
  <main class="main">
    <router-view v-slot="{ Component, route: r }">
      <component :is="Component" :key="r.fullPath" class="page" />
    </router-view>
  </main>
</template>

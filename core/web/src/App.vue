<script setup>
import { onMounted, defineAsyncComponent } from 'vue';
import { store } from './lib/store.js';
import { boot } from './lib/api.js';
import HudBackground from './components/HudBackground.vue';
import ToastHost from './components/ToastHost.vue';
import ConfirmHost from './components/ConfirmHost.vue';
import LoginPage from './pages/LoginPage.vue';
import ShellLayout from './pages/ShellLayout.vue';

// the create-tunnel window is only fetched the first time it is opened
const TunnelFormWindow = defineAsyncComponent(() => import('./pages/TunnelFormWindow.vue'));

onMounted(boot);
</script>

<template>
  <HudBackground />
  <div v-if="!store.booted" class="boot"><div class="boot-mark"></div></div>
  <LoginPage v-else-if="!store.session" />
  <ShellLayout v-else />
  <TunnelFormWindow v-if="store.session && store.newTunnel" @close="store.newTunnel = false; store.newTunnelKind = ''" />
  <ConfirmHost />
  <ToastHost />
</template>

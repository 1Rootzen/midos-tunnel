<script setup>
import { toasts } from '../lib/toast.js';
import AppIcon from './AppIcon.vue';
</script>

<template>
  <TransitionGroup name="toast" tag="div" class="toasts" role="status" aria-live="polite">
    <div v-for="t in toasts" :key="t.id" class="toast glass" :class="t.kind">
      <AppIcon :name="t.kind === 'err' ? 'alert' : 'check'" draw />
      <span>{{ t.text }}</span>
    </div>
  </TransitionGroup>
</template>
